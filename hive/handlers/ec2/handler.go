package handlers_ec2

// EC2Handler defines the interface for EC2 operation handlers
type EC2Handler interface {
	// Topic returns the NATS topic this handler subscribes to
	Topic() string

	// Process handles the business logic for the operation
	// Takes JSON input and returns JSON output or error payload
	Process(jsonData []byte) []byte
}
