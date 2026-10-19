package service

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/model"
)

func init() {
	model.RegisterUserHomeSurface(model.UserHomeSurface{
		Name: "test-surface",
		Paths: map[model.SurfaceLink]string{
			model.SurfaceLinkWallet:   "/product/account/billing",
			model.SurfaceLinkSettings: "/product/account",
		},
	})
}

func TestUserMailLinkFollowsHomeSurface(t *testing.T) {
	t.Setenv("FRONTEND_BASE_URL", "https://lazu.ai")
	for _, tc := range []struct {
		user *model.User
		link model.SurfaceLink
		want string
	}{
		{user: &model.User{HomeSurface: "test-surface"}, link: model.SurfaceLinkWallet, want: "https://lazu.ai/product/account/billing"},
		{user: &model.User{HomeSurface: "test-surface"}, link: model.SurfaceLinkSettings, want: "https://lazu.ai/product/account"},
		{user: &model.User{}, link: model.SurfaceLinkWallet, want: "https://lazu.ai/console/wallet"},
		{user: &model.User{}, link: model.SurfaceLinkSettings, want: "https://lazu.ai/console/personal"},
		{user: nil, link: model.SurfaceLinkWallet, want: "https://lazu.ai/console/wallet"},
	} {
		if got := userMailLink(tc.user, tc.link); got != tc.want {
			t.Fatalf("userMailLink(%+v, %s) = %s, want %s", tc.user, tc.link, got, tc.want)
		}
	}
}
