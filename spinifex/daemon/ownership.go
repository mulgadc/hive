package daemon

import (
	"log/slog"

	"github.com/mulgadc/spinifex/spinifex/awserrors"
	"github.com/mulgadc/spinifex/spinifex/utils"
	"github.com/nats-io/nats.go"
)

// checkInstanceOwnership verifies the caller owns the instance.
// Returns true if access is allowed, false if denied (error already sent).
// Pre-Phase4 instances (empty AccountID) are only visible to root (GlobalAccountID).
func checkInstanceOwnership(msg *nats.Msg, instanceID, ownerAccountID string) bool {
	callerAccountID := utils.AccountIDFromMsg(msg)

	// Pre-Phase4 instance: only root can access
	if ownerAccountID == "" {
		if callerAccountID != utils.GlobalAccountID {
			slog.Warn("Pre-Phase4 instance access denied (not root)",
				"instanceId", instanceID, "callerAccount", callerAccountID)
			respondWithError(msg, awserrors.ErrorInvalidInstanceIDNotFound)
			return false
		}
		return true
	}

	// Normal ownership check
	if callerAccountID != ownerAccountID {
		slog.Warn("Account does not own instance",
			"instanceId", instanceID, "callerAccount", callerAccountID, "ownerAccount", ownerAccountID)
		respondWithError(msg, awserrors.ErrorInvalidInstanceIDNotFound)
		return false
	}
	return true
}

// isInstanceVisible checks if the caller can see this instance (for Describe operations).
// Pre-Phase4 instances (empty AccountID) are only visible to root (GlobalAccountID).
func isInstanceVisible(callerAccountID, ownerAccountID string) bool {
	if ownerAccountID == "" {
		return callerAccountID == utils.GlobalAccountID
	}
	return callerAccountID == ownerAccountID
}
