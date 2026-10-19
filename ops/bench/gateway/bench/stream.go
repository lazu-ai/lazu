package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
)

type fixtureStreamFacts struct {
	first, terminal float64
	events, content int
	readErr         error
	unfinished      bool
}

func readFixtureStream(body io.Reader, now func() float64) fixtureStreamFacts {
	var facts fixtureStreamFacts
	reader := bufio.NewReaderSize(body, 64<<10)
	var data string
	for {
		line, err := reader.ReadString('\n')
		if strings.HasPrefix(line, "data:") {
			data += strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		} else if (line == "\n" || line == "\r\n") && data != "" {
			facts.events++
			at := now()
			if strings.Contains(data, `"delta"`) && strings.Contains(data, "tok") {
				facts.content++
				if facts.first == 0 {
					facts.first = at
				}
			}
			if data == "[DONE]" || strings.Contains(data, `"response.completed"`) {
				facts.terminal = at
			}
			data = ""
		}
		if err != nil {
			facts.unfinished = data != ""
			if !errors.Is(err, io.EOF) {
				facts.readErr = err
			}
			return facts
		}
	}
}

func (facts fixtureStreamFacts) completionError(stream bool, expected int) error {
	if facts.readErr != nil {
		return fmt.Errorf("response body read failed: %w", facts.readErr)
	}
	if !stream {
		return nil
	}
	if facts.unfinished || facts.first == 0 || facts.terminal == 0 {
		return errors.New("incomplete stream: content and a complete terminal SSE event are required")
	}
	if expected > 0 && facts.content != expected {
		return fmt.Errorf("incomplete stream: %d content events, expected %d", facts.content, expected)
	}
	return nil
}
