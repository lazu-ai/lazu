package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/model"
)

// TokenResolver lets an extension authenticate its own server-originated
// calls before the X-Token-Id header is read. It reports handled=false to
// leave the request to the core.
type TokenResolver func(c *gin.Context) (token *model.Token, handled bool, err error)

var tokenResolvers []TokenResolver

// RegisterTokenResolver adds a resolver; resolvers run in registration order.
func RegisterTokenResolver(resolver TokenResolver) {
	tokenResolvers = append(tokenResolvers, resolver)
}

func resolveExtensionToken(c *gin.Context) (*model.Token, bool, error) {
	for _, resolve := range tokenResolvers {
		if token, handled, err := resolve(c); handled {
			return token, true, err
		}
	}
	return nil, false, nil
}
