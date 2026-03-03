package policy

// IAMAction converts a gateway service name and action name into the
// IAM policy action format "service:ActionName".
// For example: ("ec2", "RunInstances") -> "ec2:RunInstances"
func IAMAction(service, action string) string {
	return service + ":" + action
}
