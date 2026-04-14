package wechat

import (
	"encoding/json"
	"fmt"

	"github.com/benenen/myclaw/internal/channel"
)

func BuildReplyTarget(recipientID string, accountUID string, credentialPayload []byte) (channel.ReplyTarget, error) {
	payload := map[string]any{}
	if len(credentialPayload) > 0 {
		if err := json.Unmarshal(credentialPayload, &payload); err != nil {
			return channel.ReplyTarget{}, fmt.Errorf("unmarshal wechat credential payload: %w", err)
		}
	}
	baseURL, _ := payload["baseurl"].(string)
	botToken, _ := payload["bot_token"].(string)
	wechatUIN, _ := payload["wechat_uin"].(string)
	if wechatUIN == "" {
		wechatUIN = randomWechatUIN()
	}
	return channel.ReplyTarget{
		ChannelType: "wechat",
		RecipientID: recipientID,
		Metadata: map[string]string{
			"account_uid": accountUID,
			"base_url":    baseURL,
			"token":       botToken,
			"wechat_uin":  wechatUIN,
		},
	}, nil
}
