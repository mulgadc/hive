package gateway

type ErrorMessage struct {
	HTTPCode int
	Message  string
}

// TODO: This list is incomplete and needs to be expanded
var ErrorLookup = map[string]ErrorMessage{

	"AuthFailure":          {HTTPCode: 403, Message: "The AWS was not able to validate the provided access credentials"},
	"AccessDenied":         {HTTPCode: 403, Message: "The AWS was not able to validate the provided access credentials"},
	"Blocked":              {HTTPCode: 403, Message: "The request was blocked. This can happen if your request is suspected to be fraudulent or otherwise illegal."},
	"DependencyViolation":  {HTTPCode: 400, Message: "The request depends on another action which has not yet completed successfully."},
	"DryRunOperation":      {HTTPCode: 412, Message: "The request would have succeeded, but the DryRun flag was set."},
	"InternalError":        {HTTPCode: 500, Message: "The request processing has failed because of an unknown error, exception or failure."},
	"InvalidAction":        {HTTPCode: 400, Message: "The action or operation requested is invalid. Verify that the action is typed correctly."},
	"InvalidClientTokenId": {HTTPCode: 403, Message: "The X.509 certificate or the AWS access key ID provided does not exist in our records."},
	"InvalidParameter":     {HTTPCode: 400, Message: "The parameter name or value is invalid."},
	"InvalidParameterCombination": {
		HTTPCode: 400,
		Message:  "Parameters that must not be used together were used together.",
	},
	"InvalidParameterValue": {HTTPCode: 400, Message: "The value for a parameter is invalid."},
	"InvalidQueryParameter": {HTTPCode: 400, Message: "The query string contains a syntax error or unsupported parameter."},
	"MalformedQueryString":  {HTTPCode: 400, Message: "The query string contains a syntax error."},
	"MissingAction":         {HTTPCode: 400, Message: "The request is missing an action or operation parameter."},
	"MissingAuthenticationToken": {
		HTTPCode: 403,
		Message:  "The request must contain either a valid (registered) AWS access key ID or X.509 certificate.",
	},
	"MissingParameter":   {HTTPCode: 400, Message: "A required parameter for the specified action is not supplied."},
	"OptInRequired":      {HTTPCode: 403, Message: "The AWS account is not signed up for the service."},
	"RequestExpired":     {HTTPCode: 403, Message: "The request has expired."},
	"ServiceUnavailable": {HTTPCode: 503, Message: "The request has failed due to a temporary failure of the server."},
	"Throttling":         {HTTPCode: 429, Message: "The request was denied because it exceeded the request rate limit."},
	"UnauthorizedOperation": {
		HTTPCode: 403,
		Message:  "You are not authorized to perform this operation.",
	},
	"UnsupportedOperation": {HTTPCode: 400, Message: "The action requested is not supported."},
}
