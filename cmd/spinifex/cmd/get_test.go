package cmd

import (
	"testing"

	"github.com/mulgadc/spinifex/spinifex/types"
	"github.com/stretchr/testify/assert"
)

func TestFormatRoles(t *testing.T) {
	tests := []struct {
		name string
		resp types.NodeStatusResponse
		want string
	}{
		{
			name: "no roles",
			resp: types.NodeStatusResponse{},
			want: "-",
		},
		{
			name: "nats leader only",
			resp: types.NodeStatusResponse{NATSRole: "leader"},
			want: "nats:leader",
		},
		{
			name: "predastore follower only",
			resp: types.NodeStatusResponse{PredastoreRole: "follower"},
			want: "predastore:follower",
		},
		{
			name: "both roles",
			resp: types.NodeStatusResponse{NATSRole: "leader", PredastoreRole: "follower"},
			want: "nats:leader,predastore:follower",
		},
		{
			name: "both leaders",
			resp: types.NodeStatusResponse{NATSRole: "leader", PredastoreRole: "leader"},
			want: "nats:leader,predastore:leader",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatRoles(tt.resp)
			assert.Equal(t, tt.want, got)
		})
	}
}
