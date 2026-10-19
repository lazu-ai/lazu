package model

import (
	"net/url"
	"strings"
)

// An account's home surface is the product it signed up through. The empty
// surface is the console, which every older account keeps; extensions
// register other surfaces with their own pages.

// SurfaceLink names a place a message sends its reader. Each surface has its
// own page for it: another product's wallet is its own billing page, not the
// console wallet.
type SurfaceLink string

const (
	SurfaceLinkHome     SurfaceLink = "home"
	SurfaceLinkWallet   SurfaceLink = "wallet"
	SurfaceLinkSettings SurfaceLink = "settings"
	SurfaceLinkUsage    SurfaceLink = "usage"
	SurfaceLinkSite     SurfaceLink = "site"
)

// UserHomeSurface describes a surface an extension adds.
type UserHomeSurface struct {
	// Name is stored in users.home_surface and sent by its sign-up pages.
	Name string
	// Paths are the site-relative pages for each link; a missing link falls
	// back to the console home.
	Paths map[SurfaceLink]string
	// ProjectlessTopUpLinks: the surface's wallet takes only ?top_up_id=,
	// not the console's project_id.
	ProjectlessTopUpLinks bool
	// CheckoutSuccessPath gets ?top_up_id= appended; CheckoutCancelPath is
	// used as is. Both are site-relative.
	CheckoutSuccessPath string
	CheckoutCancelPath  string
	// BrowserPathPrefix is the site path its pages live under (with or
	// without a locale prefix), and LoginPath its own sign-in page.
	BrowserPathPrefix string
	LoginPath         string
}

var consoleSurfacePaths = map[SurfaceLink]string{
	SurfaceLinkHome:     "/console",
	SurfaceLinkWallet:   "/console/wallet",
	SurfaceLinkSettings: "/console/personal",
	SurfaceLinkUsage:    "/console/usage",
	SurfaceLinkSite:     "/",
}

var homeSurfaces = map[string]UserHomeSurface{}

// RegisterUserHomeSurface adds a surface; register from init().
func RegisterUserHomeSurface(surface UserHomeSurface) {
	surface.Name = strings.ToLower(strings.TrimSpace(surface.Name))
	if surface.Name == "" {
		return
	}
	homeSurfaces[surface.Name] = surface
}

// LookupUserHomeSurface returns a registered surface; ok is false for the
// console.
func LookupUserHomeSurface(surface string) (UserHomeSurface, bool) {
	found, ok := homeSurfaces[NormalizeUserHomeSurface(surface)]
	return found, ok
}

// UserHomeSurfaceForBrowserPath finds the surface whose pages contain path,
// with or without a locale prefix.
func UserHomeSurfaceForBrowserPath(path string) (UserHomeSurface, bool) {
	parsed, err := url.Parse(path)
	if err != nil {
		return UserHomeSurface{}, false
	}
	route := parsed.Path
	for _, locale := range []string{"/zh-TW", "/zh", "/ja"} {
		if route == locale || strings.HasPrefix(route, locale+"/") {
			route = strings.TrimPrefix(route, locale)
			break
		}
	}
	for _, surface := range homeSurfaces {
		prefix := strings.TrimRight(surface.BrowserPathPrefix, "/")
		if prefix != "" && (route == prefix || strings.HasPrefix(route, prefix+"/")) {
			return surface, true
		}
	}
	return UserHomeSurface{}, false
}

// NormalizeUserHomeSurface keeps the surfaces the product knows; anything
// else is the console.
func NormalizeUserHomeSurface(surface string) string {
	surface = strings.ToLower(strings.TrimSpace(surface))
	if _, ok := homeSurfaces[surface]; ok {
		return surface
	}
	return ""
}

// UserSurfacePath is the site-relative path of link on surface.
func UserSurfacePath(surface string, link SurfaceLink) string {
	paths := consoleSurfacePaths
	if found, ok := LookupUserHomeSurface(surface); ok {
		paths = found.Paths
	}
	if path, ok := paths[link]; ok {
		return path
	}
	return consoleSurfacePaths[SurfaceLinkHome]
}

// UserSurfaceTopUpPath is the wallet link a payment message carries: it
// opens the reader's wallet on the top-up it is about.
func UserSurfaceTopUpPath(surface string, projectPublicID string, topUpPublicID string) string {
	if found, ok := LookupUserHomeSurface(surface); ok && found.ProjectlessTopUpLinks {
		return UserSurfacePath(surface, SurfaceLinkWallet) + "?top_up_id=" + url.QueryEscape(topUpPublicID)
	}
	return UserSurfacePath(surface, SurfaceLinkWallet) + "?project_id=" + url.QueryEscape(projectPublicID) +
		"&top_up_id=" + url.QueryEscape(topUpPublicID)
}

// GetUserHomeSurface reads an account's surface. A missing account or an
// unavailable database reads as the console, which is always a valid answer.
func GetUserHomeSurface(userID int) string {
	if CoreDB() == nil || userID <= 0 {
		return ""
	}
	var surfaces []string
	if err := CoreDB().Model(&User{}).Where("id = ?", userID).Limit(1).Pluck("home_surface", &surfaces).Error; err != nil || len(surfaces) == 0 {
		return ""
	}
	return NormalizeUserHomeSurface(surfaces[0])
}

// SetUserHomeSurface records the surface a new account signed up through.
// Only a console account moves: the surface is set once, at registration.
func SetUserHomeSurface(userID int, surface string) error {
	surface = NormalizeUserHomeSurface(surface)
	if CoreDB() == nil || userID <= 0 || surface == "" {
		return nil
	}
	return CoreDB().Model(&User{}).
		Where("id = ? AND home_surface = ?", userID, "").
		Update("home_surface", surface).Error
}
