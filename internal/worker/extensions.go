package worker

var extensionTasks []func() []Task

// RegisterTasks adds tasks an extension runs in the worker, in both the
// long-running and the one-shot modes.
func RegisterTasks(tasks func() []Task) {
	extensionTasks = append(extensionTasks, tasks)
}

// ExtensionTasks builds every registered extension task.
func ExtensionTasks() []Task {
	var tasks []Task
	for _, build := range extensionTasks {
		tasks = append(tasks, build()...)
	}
	return tasks
}
