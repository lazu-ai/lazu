package common

type backgroundLoop struct {
	name string
	run  func()
}

var backgroundLoops []backgroundLoop

// RegisterBackgroundLoop adds a long-running loop the API process starts
// with its other maintenance goroutines.
func RegisterBackgroundLoop(name string, run func()) {
	backgroundLoops = append(backgroundLoops, backgroundLoop{name: name, run: run})
}

// StartBackgroundLoops starts every registered loop under SafeGo.
func StartBackgroundLoops() {
	for _, loop := range backgroundLoops {
		SafeGo(loop.name, loop.run)
	}
}
