package controller

import "github.com/lazu-ai/lazu/internal/model"

// refreshModelMarketplaceCaches refreshes pricing data and invalidates
// community-model snapshots so model marketplace views can reflect updates immediately.
func refreshModelMarketplaceCaches() {
	model.RefreshPricing()
	invalidateCommunityModelsCache()
}
