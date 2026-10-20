package textgen

import "github.com/lazu-ai/lazu/internal/dto"

// Ending is what a streaming emitter keeps of a reply: how it ended, what it
// cost, and which calls have arguments. Not its content: the client already
// has that, and a long stream must not grow with every frame (Collector
// keeps it all, for whole replies and settlement).
type Ending struct {
	stop     StopReason
	stopSeen bool
	usage    *dto.Usage
	err      *SemanticError
	tools    bool
	args     map[int]bool
}

// Add takes the events in.
func (e *Ending) Add(events ...Event) {
	for _, event := range events {
		switch event.Kind {
		case EventStop:
			e.stop, e.stopSeen = event.Stop, true
		case EventUsage:
			e.usage = MergeUsage(e.usage, event.Usage)
		case EventError:
			e.err = event.Error
		case EventToolStart:
			e.tools = true
		case EventToolArguments, EventToolDone:
			e.tools = true
			if event.Text != "" {
				if e.args == nil {
					e.args = map[int]bool{}
				}
				e.args[event.ToolIndex] = true
			}
		}
	}
}

// ArgsSeen says whether the call at the tool index has any arguments yet.
func (e *Ending) ArgsSeen(toolIndex int) bool { return e.args[toolIndex] }

// Result is the ending as a Result without parts: the stop (a reply that
// called a tool and ended plainly stopped for the tool, as Collector says),
// whether it was seen, the usage, the error.
func (e *Ending) Result() Result {
	result := Result{Stop: e.stop, StopSeen: e.stopSeen, Usage: e.usage, Error: e.err}
	if e.tools && (!e.stopSeen || e.stop == StopEnd) {
		result.Stop = StopTool
	}
	return result
}
