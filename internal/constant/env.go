package constant

const DefaultStreamingTimeoutSeconds = 900

var StreamingTimeout int
var DifyDebug bool
var MaxFileDownloadMB int

// MaxVideoDownloadMB caps a generated video fetched from a provider; a
// 1080p clip with audio runs past the file cap that images use.
var MaxVideoDownloadMB int
var ForceStreamOption bool
var GetMediaToken bool
var GetMediaTokenNotStream bool
var AzureDefaultAPIVersion string
var GeminiVisionMaxImageNum int
var NotifyLimitCount int
var NotificationLimitDurationMinute int
var GenerateDefaultToken bool
var ErrorLogEnabled bool
