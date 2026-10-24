package telegram

import (
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
)

// Fixed replies in the asker's Telegram language (zh, ja, otherwise en).

type phraseKey int

const (
	phraseWelcome phraseKey = iota
	phraseKeyLeaked
	phraseOff
	phraseSlowDown
	phraseDailyLimit
	phraseFailed
	phraseUserDaily
	phraseLinkForMore
)

var phrases = map[phraseKey]map[string]string{
	phraseWelcome: {
		"en": "Hi! I answer questions about %s from its documentation. Ask me anything here, or mention me in the group. I cannot see accounts or balances.",
		"zh": "你好！我根据 %s 的文档回答问题。直接在这里问，或在群里 @ 我。我看不到账户和余额。",
		"ja": "こんにちは。%s のドキュメントをもとに質問に答えます。ここで質問するか、グループで私をメンションしてください。アカウントや残高は見られません。",
	},
	phraseKeyLeaked: {
		"en": "That message contains an API key. Revoke it in the console now and create a new one; never post keys in chats.",
		"zh": "这条消息里有 API Key。请马上在控制台作废它并新建一个；不要在聊天里发送 Key。",
		"ja": "このメッセージには API キーが含まれています。今すぐコンソールで無効にし、新しいキーを作成してください。チャットにキーを貼らないでください。",
	},
	phraseOff: {
		"en": "Automatic answers are not switched on here yet.",
		"zh": "这里还没有开启自动答疑。",
		"ja": "ここではまだ自動回答が有効になっていません。",
	},
	phraseSlowDown: {
		"en": "That is a lot of questions at once. Please wait a few minutes and ask again.",
		"zh": "问得有点快，请过几分钟再问。",
		"ja": "質問が続いています。数分おいてからもう一度どうぞ。",
	},
	phraseDailyLimit: {
		"en": "Today's automatic answers are used up. Please try again tomorrow or write to support.",
		"zh": "今天的自动答疑次数已经用完，请明天再问，或者写信给客服。",
		"ja": "本日の自動回答の上限に達しました。明日もう一度試すか、サポートにご連絡ください。",
	},
	phraseUserDaily: {
		"en": "You have used today's questions. Please come back tomorrow, or write to support.",
		"zh": "你今天的提问次数已经用完了，明天再来，或者写信给客服。",
		"ja": "本日の質問回数の上限に達しました。明日またどうぞ。お急ぎの場合はサポートにご連絡ください。",
	},
	phraseLinkForMore: { // %d: the daily allowance
		"en": "Without a linked account, private chats get %d questions a day. Link Telegram in the console to keep asking, or mention me in the community group.",
		"zh": "未绑定账号时，私聊每天可以问 %d 次。在控制台绑定 Telegram 后可以继续提问，也可以到社群里 @我。",
		"ja": "アカウント未連携の場合、プライベートチャットでの質問は 1 日 %d 回までです。コンソールで Telegram を連携すると続けて質問でき、コミュニティでメンションしても質問できます。",
	},
	phraseFailed: {
		"en": "I could not answer that just now. Please try again in a moment.",
		"zh": "暂时没能回答，请稍后再试。",
		"ja": "いまは回答できませんでした。少し時間をおいてもう一度お試しください。",
	},
}

// lang maps a Telegram language code to en, zh or ja.
func lang(msg *IncomingMessage) string {
	code := ""
	if msg != nil && msg.From != nil {
		code = strings.ToLower(msg.From.LanguageCode)
	}
	switch {
	case strings.HasPrefix(code, "zh"):
		return "zh"
	case strings.HasPrefix(code, "ja"):
		return "ja"
	default:
		return "en"
	}
}

// phrase is a fixed reply, HTML-escaped and ready to send.
func phrase(msg *IncomingMessage, key phraseKey) string {
	text := phrases[key][lang(msg)]
	if key == phraseWelcome {
		brand := strings.TrimSpace(common.SystemName)
		if brand == "" {
			brand = "this service"
		}
		text = strings.Replace(text, "%s", brand, 1)
	}
	return Escape(text)
}
