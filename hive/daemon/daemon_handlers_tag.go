package daemon

import (
	"strings"

	"github.com/nats-io/nats.go"
)

func (d *Daemon) handleEC2CreateTags(msg *nats.Msg) {
	handleNATSRequest(msg, d.tagsService.CreateTags)
}

func (d *Daemon) handleEC2DeleteTags(msg *nats.Msg) {
	handleNATSRequest(msg, d.tagsService.DeleteTags)
}

func (d *Daemon) handleEC2DescribeTags(msg *nats.Msg) {
	handleNATSRequest(msg, d.tagsService.DescribeTags)
}

// ownsResource checks whether the given account owns the specified resource.
// Used by the tags service to validate resource ownership before applying tags.
func (d *Daemon) ownsResource(accountID, resourceID string) bool {
	switch {
	case strings.HasPrefix(resourceID, "i-"):
		d.Instances.Mu.Lock()
		instance, ok := d.Instances.VMS[resourceID]
		d.Instances.Mu.Unlock()
		if !ok {
			return false
		}
		// Allow pre-Phase4 instances (empty AccountID) for backward compatibility
		return instance.AccountID == "" || instance.AccountID == accountID
	default:
		// KV-backed resources (VPCs, IGWs, EIGWs) already store tags per-account.
		// S3-backed resources (volumes, images, snapshots) would require an S3 read.
		// Allow by default — the primary risk (instance cross-account tagging) is covered above.
		return true
	}
}
