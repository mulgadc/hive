package qmp

import (
	"encoding/json"
	"net"
	"sync"
)

type unmarshalTarget interface{}

var CommandResponseTypes = map[string]unmarshalTarget{
	"stop":             &json.RawMessage{},
	"cont":             &json.RawMessage{},
	"system_powerdown": &json.RawMessage{},
	"system_reset":     &json.RawMessage{},
	"system_wakeup":    &json.RawMessage{},

	"query-block": &[]BlockDevice{},

	"query-status": &Status{},
}

type Command struct {
	ID         string     `json:"id"`
	QMPCommand QMPCommand `json:"command"`
	Attributes Attributes `json:"attributes"`
}

type Attributes struct {
	StopInstance   bool `json:"stop_instance"`
	DeleteInstance bool `json:"delete_instance"`
}

// (QEMU) stop / cont (resume) / system_powerdown / system_reset / system_wakeup
type EventLifeCycleResponse struct {
	ID     string          `json:"id"`
	Return json.RawMessage `json:"return"`
	Error  *QMPError       `json:"error,omitempty"`
}

// (QEMU) query-status
// {"return": {"status": "running", "singlestep": false, "running": true}}
type EventQueryStatusResponse struct {
	ID     string    `json:"id"`
	Return Status    `json:"return"`
	Error  *QMPError `json:"error,omitempty"`
}

type Status struct {
	Status     string `json:"status"`
	Singlestep bool   `json:"singlestep"`
	Running    bool   `json:"running"`
}

// (QEMU) query-block
// {"return": [{"io-status": "ok", "device": "os", "locked": false, "removable": false, "inserted": {"iops_rd": 0, "detect_zeroes": "off", "image": {"virtual-size": 4294967296, "filename": "nbd://127.0.0.1:44801", "format": "raw"}, "iops_wr": 0, "ro": false, "node-name": "#block192", "backing_file_depth": 0, "drv": "raw", "iops": 0, "bps_wr": 0, "write_threshold": 0, "encrypted": false, "bps": 0, "bps_rd": 0, "cache": {"no-flush": false, "direct": false, "writeback": true}, "file": "nbd://127.0.0.1:44801"}, "qdev": "/machine/peripheral-anon/device[0]/virtio-backend", "type": "unknown"}, {"io-status": "ok", "device": "cloudinit", "locked": false, "removable": false, "inserted": {"iops_rd": 0, "detect_zeroes": "off", "image": {"virtual-size": 1048576, "filename": "nbd://127.0.0.1:42911", "format": "raw"}, "iops_wr": 0, "ro": true, "node-name": "#block312", "backing_file_depth": 0, "drv": "raw", "iops": 0, "bps_wr": 0, "write_threshold": 0, "encrypted": false, "bps": 0, "bps_rd": 0, "cache": {"no-flush": false, "direct": false, "writeback": true}, "file": "nbd://127.0.0.1:42911"}, "qdev": "/machine/peripheral-anon/device[3]/virtio-backend", "type": "unknown"}, {"io-status": "ok", "device": "ide1-cd0", "locked": false, "removable": true, "qdev": "/machine/unattached/device[24]", "tray_open": false, "type": "unknown"}, {"device": "floppy0", "locked": false, "removable": true, "qdev": "/machine/unattached/device[18]", "type": "unknown"}, {"device": "sd0", "locked": false, "removable": true, "type": "unknown"}]}

type QMPQueryBlockResponse struct {
	Return []BlockDevice `json:"return"`
	Error  *QMPError     `json:"error,omitempty"`
}

type BlockDevice struct {
	IOStatus  string         `json:"io-status,omitempty"`
	Device    string         `json:"device"`
	Locked    bool           `json:"locked"`
	Removable bool           `json:"removable"`
	TrayOpen  *bool          `json:"tray_open,omitempty"`
	Inserted  *BlockInserted `json:"inserted,omitempty"`
	QDev      string         `json:"qdev,omitempty"`
	Type      string         `json:"type"`
}

type BlockInserted struct {
	IOPSRead       int        `json:"iops_rd"`
	IOPSWrite      int        `json:"iops_wr"`
	IOPS           int        `json:"iops"`
	BPSRead        int        `json:"bps_rd"`
	BPSWrite       int        `json:"bps_wr"`
	BPS            int        `json:"bps"`
	WriteThreshold int        `json:"write_threshold"`
	DetectZeroes   string     `json:"detect_zeroes"`
	RO             bool       `json:"ro"`
	NodeName       string     `json:"node-name"`
	BackingDepth   int        `json:"backing_file_depth"`
	Encrypted      bool       `json:"encrypted"`
	Driver         string     `json:"drv"`
	Image          BlockImage `json:"image"`
	File           string     `json:"file"`
	Cache          BlockCache `json:"cache"`
}

type BlockImage struct {
	VirtualSize int64  `json:"virtual-size"`
	Filename    string `json:"filename"`
	Format      string `json:"format"`
}

type BlockCache struct {
	NoFlush   bool `json:"no-flush"`
	Direct    bool `json:"direct"`
	Writeback bool `json:"writeback"`
}

type QMPError struct {
	Class string `json:"class"`
	Desc  string `json:"desc"`
}

// QMP greeting on connect
type QMPGreeting struct {
	QMP struct {
		Version struct {
			QEMU struct {
				Major int `json:"major"`
				Minor int `json:"minor"`
			} `json:"qemu"`
		} `json:"version"`
		Capabilities []string `json:"capabilities"`
	} `json:"QMP"`
}

type QMPCommand struct {
	Execute   string                 `json:"execute"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
}

type QMPResponse struct {
	Return json.RawMessage `json:"return"`
	Error  *QMPError       `json:"error,omitempty"`
}

type QMPClient struct {
	Conn    net.Conn
	Decoder *json.Decoder
	Encoder *json.Encoder
	Mu      sync.Mutex
}

func NewQMPClient(path string) (*QMPClient, error) {
	conn, err := net.Dial("unix", path)
	if err != nil {
		return nil, err
	}

	//conn.SetReadDeadline(time.Now().Add(30 * time.Second))

	client := &QMPClient{
		Conn:    conn,
		Decoder: json.NewDecoder(conn),
		Encoder: json.NewEncoder(conn),
	}

	// wait for greeting
	var greeting QMPGreeting
	if err := client.Decoder.Decode(&greeting); err != nil {
		conn.Close()
		return nil, err
	}

	// enable capabilities
	//client.Send(QMPCommand{Execute: "qmp_capabilities"})

	return client, nil
}
