package helper

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/logger"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/setting/operation_setting"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
)

const (
	InitialScannerBufferSize = 64 << 10 // 64KB (64*1024)
	MaxScannerBufferSize     = 10 << 20 // 10MB (10*1024*1024)
	DefaultPingInterval      = 15 * time.Second
	DefaultStreamingTimeout  = time.Duration(constant.DefaultStreamingTimeoutSeconds) * time.Second
)

// streamWriteTimeout bounds each callback and each held-back flush; work that
// takes longer ends the stream. A variable so tests can shorten it.
var streamWriteTimeout = 10 * time.Second

// Only the fixed initial allocation is retained. Scanner growth up to the
// existing event limit remains request-owned and is collected normally.
var streamScannerBuffers = sync.Pool{
	New: func() interface{} { return new([InitialScannerBufferSize]byte) },
}

type StreamScanResult struct {
	Done bool
	Err  error
}

func (r StreamScanResult) RelayError(responseStarted bool) *types.LazuError {
	if r.Err == nil {
		return nil
	}
	code := types.ErrorCodeUpstreamNetworkError
	if errors.Is(r.Err, context.Canceled) {
		code = types.ErrorCodeClientCanceled
	}
	if errors.Is(r.Err, context.DeadlineExceeded) {
		code = types.ErrorCodeUpstreamTimeout
	}
	if responseStarted || code == types.ErrorCodeClientCanceled {
		return types.NewError(r.Err, code, types.ErrOptionWithSkipRetry())
	}
	return types.NewError(r.Err, code)
}

type streamScanState struct {
	sync.Mutex
	result StreamScanResult
}

func (s *streamScanState) fail(err error) {
	s.Lock()
	defer s.Unlock()
	if s.result.Err == nil {
		s.result.Err = err
	}
}

func StreamScannerHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo, dataHandler func(data string) bool) StreamScanResult {
	return StreamScannerHandlerWithTerminal(c, resp, info, dataHandler, nil)
}

// StreamScannerHandlerWithTerminal distinguishes a parsed protocol terminal
// from an ordinary callback stop. terminal is consulted only after the callback
// returned false and finished all usage/metadata work. It describes transport
// completion, including an in-band failure, rather than billing success.
func StreamScannerHandlerWithTerminal(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo, dataHandler func(data string) bool, terminal func() bool) StreamScanResult {
	state := &streamScanState{}
	streamScannerHandler(c, resp, info, dataHandler, terminal, state)
	state.Lock()
	defer state.Unlock()
	return state.result
}

func streamScannerHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo, dataHandler func(data string) bool, terminal func() bool, state *streamScanState) {

	if resp == nil || dataHandler == nil {
		return
	}

	streamingTimeout := time.Duration(constant.StreamingTimeout) * time.Second
	if streamingTimeout <= 0 {
		streamingTimeout = DefaultStreamingTimeout
		logger.LogWarn(c, fmt.Sprintf("invalid streaming timeout %d, fallback to %s", constant.StreamingTimeout, DefaultStreamingTimeout))
	}

	var (
		stopChan   = make(chan struct{})
		stopOnce   sync.Once
		scanner    = bufio.NewScanner(resp.Body)
		ticker     = time.NewTicker(streamingTimeout)
		pingTicker *time.Ticker
		writeMutex sync.Mutex     // Mutex to protect concurrent writes
		wg         sync.WaitGroup // 用于等待所有 goroutine 退出
	)

	generalSettings := operation_setting.GetGeneralSetting()
	pingEnabled := generalSettings.PingIntervalEnabled && !info.DisablePing
	pingInterval := time.Duration(generalSettings.PingIntervalSeconds) * time.Second
	if pingInterval <= 0 {
		pingInterval = DefaultPingInterval
	}

	if pingEnabled {
		pingTicker = time.NewTicker(pingInterval)
	}

	if common.DebugEnabled {
		// print timeout and ping interval for debugging
		println("relay timeout seconds:", common.RelayTimeout)
		println("streaming timeout seconds:", int64(streamingTimeout.Seconds()))
		println("ping interval seconds:", int64(pingInterval.Seconds()))
	}

	// Broadcast completion: a ping reader must not consume the scanner
	// notification before the main waiter sees it.
	stop := func() { stopOnce.Do(func() { close(stopChan) }) }

	// 改进资源清理，确保所有 goroutine 正确退出
	defer func() {
		state.Lock()
		completed := state.result.Done && state.result.Err == nil
		state.Unlock()
		// Failures must unblock a scanner before joining. A protocol terminal
		// already stopped the reader; retain its transfer EOF for bounded reuse.
		if !completed && resp.Body != nil {
			_ = resp.Body.Close()
		}
		// 通知所有 goroutine 停止
		stop()

		ticker.Stop()
		if pingTicker != nil {
			pingTicker.Stop()
		}

		// 等待所有 goroutine 退出，最多等待5秒
		done := make(chan struct{})
		common.SafeGo("stream-scanner-wait", func() {
			wg.Wait()
			close(done)
		})

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			logger.LogError(c, "timeout waiting for goroutines to exit")
			_ = resp.Body.Close()
			return
		}
		if completed {
			service.RecycleCompletedResponse(resp)
		}
	}()

	// Headers alone do not start a stream: an upstream can answer 200 and
	// then send nothing but keep-alive comments until it reports a failure.
	// Until the first data event the stream start deadline still applies;
	// after it, only the idle timeout does.
	var startDeadline <-chan time.Time
	startTimer := time.NewTimer(0)
	startTimer.Stop()
	if !info.StreamStartDeadline.IsZero() {
		startTimer.Reset(time.Until(info.StreamStartDeadline))
		startDeadline = startTimer.C
	}
	defer startTimer.Stop()

	// moreBuffered says whether another complete line is already in the
	// scanner's buffer after the current one, so the next Scan will not wait
	// on the network. While it holds, flushes are held back and the writes
	// they cover go out with the next one.
	moreBuffered := false
	scanner.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		advance, token, err := bufio.ScanLines(data, atEOF)
		if token != nil {
			moreBuffered = advance < len(data) && bytes.IndexByte(data[advance:], '\n') >= 0
		}
		return advance, token, err
	})
	flushes := &streamFlushCoalescer{}
	firstEventSent := false
	c.Set(streamFlushCoalescerKey, flushes)
	// Writes after the scanner returns (final usage, [DONE]) flush as usual.
	defer c.Set(streamFlushCoalescerKey, (*streamFlushCoalescer)(nil))
	SetEventStreamHeaders(c)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle ping data sending with improved error handling
	if pingEnabled && pingTicker != nil {
		wg.Add(1)
		common.SafeCtxGo(ctx, "stream-scanner-ping", func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					logger.LogError(c, fmt.Sprintf("ping goroutine panic: %v", r))
					stop()
				}
				if common.DebugEnabled {
					println("ping goroutine exited")
				}
			}()

			// 添加超时保护，防止 goroutine 无限运行
			maxPingDuration := 30 * time.Minute // 最大 ping 持续时间
			pingTimeout := time.NewTimer(maxPingDuration)
			defer pingTimeout.Stop()

			for {
				select {
				case <-pingTicker.C:
					// 使用超时机制防止写操作阻塞
					done := make(chan error, 1)
					common.SafeCtxGo(ctx, "stream-scanner-ping-write", func() {
						writeMutex.Lock()
						defer writeMutex.Unlock()
						done <- PingData(c)
					})

					select {
					case err := <-done:
						if err != nil {
							logger.LogError(c, "ping data error: "+err.Error())
							return
						}
						if common.DebugEnabled {
							println("ping data sent")
						}
					case <-time.After(10 * time.Second):
						logger.LogError(c, "ping data send timeout")
						return
					case <-ctx.Done():
						return
					case <-stopChan:
						return
					}
				case <-ctx.Done():
					return
				case <-stopChan:
					return
				case <-c.Request.Context().Done():
					// 监听客户端断开连接
					return
				case <-pingTimeout.C:
					logger.LogError(c, "ping goroutine max duration reached")
					return
				}
			}
		})
	}

	// Scanner goroutine with improved error handling
	wg.Add(1)
	common.RelayCtxGo(ctx, func() {
		defer wg.Done()
		buffer := streamScannerBuffers.Get().(*[InitialScannerBufferSize]byte)
		scanner.Buffer(buffer[:], MaxScannerBufferSize)
		defer func() {
			// The outer cleanup can time out while a reader is still blocked.
			// Reuse is safe only once this reader has actually stopped.
			clear(buffer[:])
			streamScannerBuffers.Put(buffer)
		}()
		// One goroutine runs every callback of this stream, one at a time, so
		// an event costs a channel hand-off rather than a pool task. It also
		// runs the flushes held back for buffered events, so every client
		// write stays under the same 10s bound. Work that does not answer in
		// time ends this scanner, so its eventual result cannot be mistaken
		// for a later one's; closing events lets the handler goroutine exit
		// once that work returns.
		type streamWork struct {
			data       string
			deferFlush bool // more events are buffered behind this one
			flushOnly  bool // write out what earlier events held back
		}
		handlerDone := make(chan bool, 1)
		events := make(chan streamWork)
		defer close(events)
		common.SafeCtxGo(ctx, "stream-scanner-data-handler", func() {
			for work := range events {
				handlerDone <- func() bool {
					writeMutex.Lock()
					defer writeMutex.Unlock()
					if work.flushOnly {
						if flushes.pending {
							_ = FlushWriter(c)
						}
						return true
					}
					flushes.deferring = work.deferFlush
					defer func() { flushes.deferring = false }()
					return dataHandler(work.data)
				}()
			}
		})
		handlerTimeout := time.NewTimer(streamWriteTimeout)
		handlerTimeout.Stop()
		defer handlerTimeout.Stop()
		// runTimed hands one unit of work to the handler goroutine and waits
		// for its result. ok is false when the scanner has to stop.
		runTimed := func(work streamWork) (result, ok bool) {
			handlerTimeout.Reset(streamWriteTimeout)
			select {
			case events <- work:
			case <-handlerTimeout.C:
				logger.LogError(c, "data handler timeout")
				state.fail(context.DeadlineExceeded)
				return false, false
			case <-ctx.Done():
				return false, false
			case <-stopChan:
				return false, false
			}
			select {
			case result = <-handlerDone:
				if !handlerTimeout.Stop() {
					select {
					case <-handlerTimeout.C:
					default:
					}
				}
				return result, true
			case <-handlerTimeout.C:
				logger.LogError(c, "data handler timeout")
				state.fail(context.DeadlineExceeded)
				return false, false
			case <-ctx.Done():
				return false, false
			case <-stopChan:
				return false, false
			}
		}
		// flushOwed: an event was handled while more were buffered, so its
		// output may still be held back. flushPending writes it out once the
		// next line has to come from the network; false means stop.
		flushOwed := false
		flushPending := func() bool {
			if moreBuffered || !flushOwed {
				return true
			}
			flushOwed = false
			_, ok := runTimed(streamWork{flushOnly: true})
			return ok
		}
		defer func() {
			if r := recover(); r != nil {
				logger.LogError(c, fmt.Sprintf("scanner goroutine panic: %v", r))
				state.fail(fmt.Errorf("stream scanner panic: %v", r))
			}
			stop()
			if common.DebugEnabled {
				println("scanner goroutine exited")
			}
		}()

		for scanner.Scan() {
			// 检查是否需要停止
			select {
			case <-stopChan:
				return
			case <-ctx.Done():
				return
			case <-c.Request.Context().Done():
				return
			default:
			}

			ticker.Reset(streamingTimeout)
			data := scanner.Text()
			if common.DebugEnabled {
				println(data)
			}

			if len(data) < 6 {
				if !flushPending() {
					return
				}
				continue
			}
			if data[:5] != "data:" && data[:6] != "[DONE]" {
				if !flushPending() {
					return
				}
				continue
			}
			startTimer.Stop()
			data = data[5:]
			data = strings.TrimLeft(data, " ")
			data = strings.TrimSuffix(data, "\r")
			if !strings.HasPrefix(data, "[DONE]") {
				info.SetFirstResponseTime()
				// Measured here rather than at the ticker reset above, and only
				// for events that carry output: pings, blank lines and metadata
				// frames all arrive on time on a stream that is producing nothing.
				if StreamEventCarriesOutput(data) {
					info.MarkOutputChunk(StreamEventCarriesAnswer(data))
				}

				// 使用超时机制防止写操作阻塞
				// The first event always goes out at once: time to first
				// token must not wait on the rest of a batch.
				deferFlush := moreBuffered && firstEventSent
				firstEventSent = true
				success, ok := runTimed(streamWork{data: data, deferFlush: deferFlush})
				if !ok {
					return
				}
				flushOwed = flushOwed || deferFlush
				if !success {
					moreBuffered = false
					if !flushPending() {
						return
					}
					if terminal != nil && terminal() {
						state.Lock()
						if err := c.Request.Context().Err(); err != nil {
							state.result.Err = err
							state.result.Done = false
						} else {
							state.result.Done = true
						}
						state.Unlock()
					}
					return
				}
				if !flushPending() {
					return
				}
			} else {
				moreBuffered = false
				if !flushPending() {
					return
				}
				state.Lock()
				state.result.Done = true
				state.Unlock()
				// done, 处理完成标志，直接退出停止读取剩余数据防止出错
				if common.DebugEnabled {
					println("received [DONE], stopping scanner")
				}
				return
			}
		}

		moreBuffered = false
		flushPending()
		if err := scanner.Err(); err != nil {
			if err != io.EOF {
				logger.LogError(c, "scanner error: "+err.Error())
				state.fail(err)
			}
		}
	})

	// 主循环等待完成或超时
	select {
	case <-ticker.C:
		// 超时处理逻辑
		logger.LogError(c, "streaming timeout")
		state.fail(context.DeadlineExceeded)
	case <-startDeadline:
		logger.LogError(c, "upstream sent no data event before the stream start deadline")
		state.fail(relaycommon.ErrStreamStartDeadline)
	case <-stopChan:
		// 正常结束
		logger.LogInfo(c, "streaming finished")
	case <-c.Request.Context().Done():
		// 客户端断开连接
		logger.LogInfo(c, "client disconnected")
		state.fail(c.Request.Context().Err())
	}
}
