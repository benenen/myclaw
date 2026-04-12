package dto

import "time"

type ChannelAccountItem struct {
	ID          string     `json:"id"`
	ChannelType string     `json:"channel_type"`
	AccountUID  string     `json:"account_uid"`
	DisplayName string     `json:"display_name"`
	AvatarURL   string     `json:"avatar_url"`
	LastBoundAt *time.Time `json:"last_bound_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}
