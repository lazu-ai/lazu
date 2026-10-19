package constant

const (
	ChannelTypeUnknown = 0
	ChannelTypeOpenAI  = 1
	// Reserved slots are provider-cleanup tombstones. ChannelType values are
	// persisted in DB rows, so these IDs must not be reused or renumbered.
	channelTypeReserved2  = 2
	channelTypeReserved3  = 3
	channelTypeReserved4  = 4
	channelTypeReserved5  = 5
	channelTypeReserved6  = 6
	channelTypeReserved7  = 7
	ChannelTypeCustom     = 8
	channelTypeReserved9  = 9
	channelTypeReserved10 = 10
	channelTypeReserved11 = 11
	channelTypeReserved12 = 12
	channelTypeReserved13 = 13
	ChannelTypeAnthropic  = 14
	channelTypeReserved15 = 15
	channelTypeReserved16 = 16
	channelTypeReserved17 = 17
	channelTypeReserved18 = 18
	channelTypeReserved19 = 19
	channelTypeReserved20 = 20
	channelTypeReserved21 = 21
	channelTypeReserved22 = 22
	channelTypeReserved23 = 23
	ChannelTypeGemini     = 24
	channelTypeReserved25 = 25
	channelTypeReserved26 = 26
	channelTypeReserved27 = 27
	channelTypeReserved28 = 28
	channelTypeReserved29 = 29
	channelTypeReserved30 = 30
	channelTypeReserved31 = 31
	channelTypeReserved32 = 32
	channelTypeReserved33 = 33
	channelTypeReserved34 = 34
	channelTypeReserved35 = 35
	channelTypeReserved36 = 36
	channelTypeReserved37 = 37
	channelTypeReserved38 = 38
	channelTypeReserved39 = 39
	channelTypeReserved40 = 40
	channelTypeReserved41 = 41
	channelTypeReserved42 = 42
	channelTypeReserved43 = 43
	channelTypeReserved44 = 44
	channelTypeReserved45 = 45
	channelTypeReserved46 = 46
	channelTypeReserved47 = 47
	ChannelTypeXai        = 48
	channelTypeReserved49 = 49
	channelTypeReserved50 = 50
	channelTypeReserved51 = 51
	channelTypeReserved52 = 52
	channelTypeReserved53 = 53
	channelTypeReserved54 = 54
	channelTypeReserved55 = 55
	channelTypeReserved56 = 56
	channelTypeReserved57 = 57
	channelTypeReserved58 = 58
	channelTypeReserved59 = 59
	ChannelTypeTavily     = 60
	ChannelTypeExa        = 61
	ChannelTypeJina       = 62
	ChannelTypeSerper     = 63
	ChannelTypeBrave      = 64
	ChannelTypeDummy      // this one is only for count, do not add any channel after this

)

var ChannelBaseURLs = []string{
	"",                       // 0
	"https://api.openai.com", // 1
	// Empty entries mirror the reserved ChannelType IDs above; keep slot count
	// and indexes stable even when no active provider owns the value.
	"",                          // 2
	"",                          // 3
	"",                          // 4
	"",                          // 5
	"",                          // 6
	"",                          // 7
	"",                          // 8
	"",                          // 9
	"",                          // 10
	"",                          // 11
	"",                          // 12
	"",                          // 13
	"https://api.anthropic.com", // 14
	"",                          // 15
	"",                          // 16
	"",                          // 17
	"",                          // 18
	"",                          // 19
	"",                          // 20
	"",                          // 21
	"",                          // 22
	"",                          // 23
	"https://generativelanguage.googleapis.com", // 24
	"",                 // 25
	"",                 // 26
	"",                 // 27
	"",                 // 28
	"",                 // 29
	"",                 // 30
	"",                 // 31
	"",                 // 32
	"",                 // 33
	"",                 // 34
	"",                 // 35
	"",                 // 36
	"",                 // 37
	"",                 // 38
	"",                 // 39
	"",                 // 40
	"",                 // 41
	"",                 // 42
	"",                 // 43
	"",                 // 44
	"",                 // 45
	"",                 // 46
	"",                 // 47
	"https://api.x.ai", // 48
	"",                 // 49
	"",                 // 50
	"",                 // 51
	"",                 // 52
	"",                 // 53
	"",                 // 54
	"",                 // 55
	"",                 // 56
	"",                 // 57
	"",                 // 58
	"",                 // 59
	"https://api.tavily.com",
	"https://api.exa.ai",
	"https://s.jina.ai",
	"https://google.serper.dev",
	"https://api.search.brave.com",
}

var ChannelTypeNames = map[int]string{
	ChannelTypeUnknown:   "Unknown",
	ChannelTypeOpenAI:    "OpenAI",
	ChannelTypeCustom:    "Custom",
	ChannelTypeAnthropic: "Anthropic",
	ChannelTypeGemini:    "Gemini",
	ChannelTypeXai:       "xAI",
	ChannelTypeTavily:    "Tavily",
	ChannelTypeExa:       "Exa",
	ChannelTypeJina:      "Jina",
	ChannelTypeSerper:    "Serper",
	ChannelTypeBrave:     "Brave Search",
}

func GetChannelTypeName(channelType int) string {
	if name, ok := ChannelTypeNames[channelType]; ok {
		return name
	}
	return "Unknown"
}

func IsSearchChannelType(channelType int) bool {
	switch channelType {
	case ChannelTypeTavily, ChannelTypeExa, ChannelTypeJina, ChannelTypeSerper, ChannelTypeBrave:
		return true
	default:
		return false
	}
}

func SearchChannelTypes() []int {
	return []int{
		ChannelTypeTavily,
		ChannelTypeExa,
		ChannelTypeJina,
		ChannelTypeSerper,
		ChannelTypeBrave,
	}
}

func SearchProviderFromChannelType(channelType int) string {
	switch channelType {
	case ChannelTypeTavily:
		return "tavily"
	case ChannelTypeExa:
		return "exa"
	case ChannelTypeJina:
		return "jina"
	case ChannelTypeSerper:
		return "serper"
	case ChannelTypeBrave:
		return "brave"
	default:
		return ""
	}
}

func SearchChannelTypeFromProvider(provider string) int {
	switch provider {
	case "tavily":
		return ChannelTypeTavily
	case "exa":
		return ChannelTypeExa
	case "jina":
		return ChannelTypeJina
	case "serper":
		return ChannelTypeSerper
	case "brave":
		return ChannelTypeBrave
	default:
		return ChannelTypeUnknown
	}
}
