package main

// extensionCommands are worker subcommands an extension adds
// (lazu-worker <name> args...). Each returns the process exit code.
var extensionCommands = map[string]func(args []string) int{}

func registerCommand(name string, run func(args []string) int) {
	extensionCommands[name] = run
}
