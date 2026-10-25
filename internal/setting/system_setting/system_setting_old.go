package system_setting

var ServerAddress = "http://localhost:3000"

// Deprecated: delete after 2027-01-24 (the Worker download proxy: WorkerUrl,
// WorkerValidKey, WorkerAllowHttpImageRequestEnabled and EnableWorker; see
// model/option_deprecations.go and docs/DEPLOYMENT.md, Deprecated settings).
var WorkerUrl = ""
var WorkerValidKey = ""
var WorkerAllowHttpImageRequestEnabled = false

func EnableWorker() bool {
	return WorkerUrl != ""
}
