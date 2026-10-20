package controller

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/publicsite"
)

// The public model catalog and model pages are rendered here rather than by
// the SPA so crawlers (and AI retrievers that do not run JavaScript) read the
// live prices. nginx proxies /models* and /blog* to these handlers with SSI
// on, which splices in the web build's header, footer and stylesheets.

var publicCatalogCache struct {
	sync.Mutex
	catalog   publicsite.Catalog
	expiresAt time.Time
}

const publicCatalogTTL = time.Minute

func publicCatalog() (publicsite.Catalog, error) {
	publicCatalogCache.Lock()
	defer publicCatalogCache.Unlock()
	if time.Now().Before(publicCatalogCache.expiresAt) && len(publicCatalogCache.catalog.Models) > 0 {
		return publicCatalogCache.catalog, nil
	}
	body, err := cachedPublicModelDiscoveryJSON()
	if err != nil {
		return publicsite.Catalog{}, err
	}
	catalog, err := publicsite.ParseCatalog(body)
	if err != nil {
		return publicsite.Catalog{}, err
	}
	official := map[string][2]float64{}
	officialTiers := map[string]*publicsite.ContextTier{}
	officialCache := map[string][2]float64{}
	for _, pricing := range model.GetPricing() {
		officialCache[pricing.ModelName] = [2]float64{pricing.OfficialCacheReadPerMtok, pricing.OfficialCacheWritePerMtok}
		if tier := pricing.OfficialContextTier; tier != nil && tier.Threshold > 0 {
			officialTiers[pricing.ModelName] = &publicsite.ContextTier{Threshold: tier.Threshold, Input: tier.InputPerMtok,
				Output: tier.OutputPerMtok, CacheRead: tier.CacheReadPerMtok, CacheWrite5m: tier.CacheWrite5mPerMtok}
		}
		// Official prices are USD per million tokens; anything under a
		// thousandth of a dollar is a per-token figure that slipped through
		// and is better left unshown than shown a million times too small.
		if pricing.OfficialInputPrice >= 0.001 {
			official[pricing.ModelName] = [2]float64{pricing.OfficialInputPrice, pricing.OfficialOutputPrice}
		}
	}
	for index := range catalog.Models {
		if price, ok := official[catalog.Models[index].ID]; ok {
			catalog.Models[index].OfficialInput = price[0]
			cache := officialCache[catalog.Models[index].ID]
			if cache[0] > 0 {
				value := cache[0]
				catalog.Models[index].OfficialCacheRead = &value
			}
			if cache[1] > 0 {
				value := cache[1]
				catalog.Models[index].OfficialCacheWrite = &value
			}
			if price[1] >= 0.001 {
				catalog.Models[index].OfficialOutput = price[1]
			}
			catalog.Models[index].OfficialTier = officialTiers[catalog.Models[index].ID]
		}
	}
	// Without these dates the sitemap simply omits <lastmod>; the pages
	// themselves do not depend on them.
	if modified, err := model.ModelsLastModified(); err != nil {
		common.SysError("public catalog: reading model change dates: " + err.Error())
	} else {
		for index := range catalog.Models {
			catalog.Models[index].LastModified = modified[catalog.Models[index].ID]
		}
	}
	publicCatalogCache.catalog = catalog
	publicCatalogCache.expiresAt = time.Now().Add(publicCatalogTTL)
	return catalog, nil
}

func writePublicHTML(c *gin.Context, status int, body []byte) {
	cacheControl, cdnCacheControl := publicPageCacheHeaders(c.Request.URL.Path)
	c.Header("Cache-Control", cacheControl)
	if cdnCacheControl != "" {
		c.Header("CDN-Cache-Control", cdnCacheControl)
	}
	c.Data(status, "text/html; charset=utf-8", body)
}

func publicSiteError(c *gin.Context, err error) {
	c.Header("Cache-Control", "no-store")
	c.String(http.StatusServiceUnavailable, "The model catalog is temporarily unavailable: %v", err)
}

func publicNotFound(c *gin.Context, locale string) {
	body, err := publicsite.RenderNotFound(locale)
	if err != nil {
		c.String(http.StatusNotFound, "Not found")
		return
	}
	c.Header("Cache-Control", "no-cache")
	c.Data(http.StatusNotFound, "text/html; charset=utf-8", body)
}

// PublicModelsIndex serves /models (and its locale variants).
func PublicModelsIndex(locale string) gin.HandlerFunc {
	return func(c *gin.Context) {
		catalog, err := publicCatalog()
		if err != nil {
			publicSiteError(c, err)
			return
		}
		body, err := publicsite.RenderModelsIndex(catalog, locale)
		if err != nil {
			publicSiteError(c, err)
			return
		}
		writePublicHTML(c, http.StatusOK, body)
	}
}

// PublicModelMaker sends /models/<maker> to that maker's section.
func PublicModelMaker(locale string) gin.HandlerFunc {
	return func(c *gin.Context) {
		maker := strings.TrimSpace(c.Param("vendor"))
		if maker == "sitemap.xml" && locale == "en" {
			PublicModelsSitemap(c)
			return
		}
		c.Redirect(http.StatusFound, publicsite.LocalizedPath("/models", locale)+"#"+publicsite.MakerSlug(maker))
	}
}

// PublicModelPage serves /models/<maker>/<id> and its .md / .json siblings.
func PublicModelPage(locale string) gin.HandlerFunc {
	return func(c *gin.Context) {
		maker := c.Param("vendor")
		id := c.Param("model")
		format := "html"
		switch {
		case strings.HasSuffix(id, ".md"):
			format, id = "md", strings.TrimSuffix(id, ".md")
		case strings.HasSuffix(id, ".json"):
			format, id = "json", strings.TrimSuffix(id, ".json")
		}
		catalog, err := publicCatalog()
		if err != nil {
			publicSiteError(c, err)
			return
		}
		if replacement := model.CompatibilityModelTarget(id); replacement != "" {
			if canonical, exists := catalog.Find(replacement); exists {
				target := publicsite.LocalizedPath(publicsite.ModelPath(canonical), locale)
				if format != "html" {
					target += "." + format
				}
				c.Redirect(http.StatusMovedPermanently, target)
				return
			}
		}
		found, ok := catalog.Find(id)
		if !ok {
			publicNotFound(c, locale)
			return
		}
		canonical := publicsite.ModelPath(found)
		if publicsite.MakerSlug(found.Maker) != maker {
			target := publicsite.LocalizedPath(canonical, locale)
			if format != "html" {
				target += "." + format
			}
			c.Redirect(http.StatusMovedPermanently, target)
			return
		}
		switch format {
		case "md":
			c.Header("Cache-Control", "public, max-age=60")
			c.Data(http.StatusOK, "text/markdown; charset=utf-8", []byte(publicsite.ModelMarkdown(catalog, found)))
			return
		case "json":
			doc, err := publicsite.ModelJSON(catalog, found)
			if err != nil {
				publicSiteError(c, err)
				return
			}
			c.Header("Cache-Control", "public, max-age=60")
			c.Data(http.StatusOK, "application/json; charset=utf-8", doc)
			return
		}
		body, err := publicsite.RenderModelPage(catalog, found, locale)
		if err != nil {
			publicSiteError(c, err)
			return
		}
		writePublicHTML(c, http.StatusOK, body)
	}
}

// PublicModelsMarkdown serves /models.md.
func PublicModelsMarkdown(c *gin.Context) {
	catalog, err := publicCatalog()
	if err != nil {
		publicSiteError(c, err)
		return
	}
	c.Header("Cache-Control", "public, max-age=60")
	c.Data(http.StatusOK, "text/markdown; charset=utf-8", []byte(publicsite.ModelsMarkdown(catalog)))
}

// PublicModelsSitemap serves /models/sitemap.xml.
func PublicModelsSitemap(c *gin.Context) {
	catalog, err := publicCatalog()
	if err != nil {
		publicSiteError(c, err)
		return
	}
	c.Header("Cache-Control", "public, max-age=300")
	c.Data(http.StatusOK, "application/xml; charset=utf-8", []byte(publicsite.ModelsSitemap(catalog)))
}
