package rbac

import (
	"net/http"
	"strings"

	"github.com/flanksource/commons/collections"
	"github.com/flanksource/commons/logger"
	"github.com/flanksource/incident-commander/auth"
	"github.com/labstack/echo/v4"
)

const (
	errNoUserID          = "Unauthorized. User not found for RBAC"
	errMisconfiguredRBAC = "Unauthorized. RBAC policy not configured correctly"
	errAccessDenied      = "Unauthorized. Access Denied"
)

func Authorization(object, action string) func(echo.HandlerFunc) echo.HandlerFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			// Skip auth if Enforcer is not initialized
			if Enforcer == nil {
				return next(c)
			}

			userID := c.Request().Header.Get(auth.UserIDHeaderKey)
			if userID == "" {
				return c.String(http.StatusUnauthorized, errNoUserID)
			}

			// Everyone with an account is a viewer
			if action == ActionRead && Check(RoleViewer, object, action) {
				return next(c)
			}

			if isAdmin, _ := Enforcer.HasRoleForUser(userID, RoleAdmin); isAdmin {
				return next(c)
			}

			// Database action is defined via HTTP Verb and Path
			if path := c.Request().URL.Path; strings.HasPrefix(path, "/db/") {
				action = policyActionFromHTTPMethod(c.Request().Method)
				resource := strings.ReplaceAll(path, "/db/", "")

				object = postgrestDatabaseObject(resource)

				// Allow viewing of tables if access is not explicitly denied
				if action == ActionRead && !collections.Contains(dbReadDenied, object) {
					return next(c)
				}

				if object == "" || action == "" {
					logger.Debugf("Skipping RBAC since no rules are defined on table: %s", resource)
					return next(c)
				}
			}

			if object == "" || action == "" {
				return c.String(http.StatusForbidden, errMisconfiguredRBAC)
			}

			if !Check(userID, object, action) {
				return c.String(http.StatusForbidden, errAccessDenied)
			}

			return next(c)
		}
	}
}
