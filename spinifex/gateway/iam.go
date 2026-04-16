package gateway

import (
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/aws/aws-sdk-go/service/iam"
	"github.com/mulgadc/spinifex/spinifex/awsec2query"
	"github.com/mulgadc/spinifex/spinifex/awserrors"
	gateway_iam "github.com/mulgadc/spinifex/spinifex/gateway/iam"
	handlers_iam "github.com/mulgadc/spinifex/spinifex/handlers/iam"
	"github.com/mulgadc/spinifex/spinifex/utils"
)

// IAMHandler processes parsed query args and returns XML response bytes.
type IAMHandler func(action string, q map[string]string, gw *GatewayConfig, accountID string) ([]byte, error)

// iamHandler creates a type-safe IAMHandler that allocates the typed input struct,
// parses query params into it, calls the handler, and marshals the output to XML.
func iamHandler[In any](handler func(string, *In, handlers_iam.IAMService) (any, error)) IAMHandler {
	return func(action string, q map[string]string, gw *GatewayConfig, accountID string) ([]byte, error) {
		input := new(In)
		if err := awsec2query.QueryParamsToStruct(q, input); err != nil {
			return nil, errors.New(awserrors.ErrorIAMInvalidInput)
		}
		output, err := handler(accountID, input, gw.IAMService)
		if err != nil {
			return nil, err
		}
		payload := utils.GenerateIAMXMLPayload(action, output)
		xmlOutput, err := utils.MarshalToXML(payload)
		if err != nil {
			return nil, errors.New(awserrors.ErrorInternalError)
		}
		return xmlOutput, nil
	}
}

// iamAccessKeyHandler is a variant of iamHandler that additionally reads the
// Spinifex-specific "ExpiresAt" extension query parameter (RFC3339, optional)
// and passes it through to the service alongside the SDK input struct.
func iamAccessKeyHandler[In any](handler func(string, *In, string, handlers_iam.IAMService) (any, error)) IAMHandler {
	return func(action string, q map[string]string, gw *GatewayConfig, accountID string) ([]byte, error) {
		input := new(In)
		if err := awsec2query.QueryParamsToStruct(q, input); err != nil {
			return nil, errors.New(awserrors.ErrorIAMInvalidInput)
		}
		output, err := handler(accountID, input, q["ExpiresAt"], gw.IAMService)
		if err != nil {
			return nil, err
		}
		payload := utils.GenerateIAMXMLPayload(action, output)
		xmlOutput, err := utils.MarshalToXML(payload)
		if err != nil {
			return nil, errors.New(awserrors.ErrorInternalError)
		}
		return xmlOutput, nil
	}
}

var iamActions = map[string]IAMHandler{
	"CreateUser": iamHandler(func(accountID string, input *iam.CreateUserInput, svc handlers_iam.IAMService) (any, error) {
		return gateway_iam.CreateUser(accountID, input, svc)
	}),
	"GetUser": iamHandler(func(accountID string, input *iam.GetUserInput, svc handlers_iam.IAMService) (any, error) {
		return gateway_iam.GetUser(accountID, input, svc)
	}),
	"ListUsers": iamHandler(func(accountID string, input *iam.ListUsersInput, svc handlers_iam.IAMService) (any, error) {
		return gateway_iam.ListUsers(accountID, input, svc)
	}),
	"DeleteUser": iamHandler(func(accountID string, input *iam.DeleteUserInput, svc handlers_iam.IAMService) (any, error) {
		return gateway_iam.DeleteUser(accountID, input, svc)
	}),
	// CreateAccessKey / UpdateAccessKey accept a Spinifex extension query parameter
	// "ExpiresAt" (RFC3339) that is not part of the AWS IAM SDK input structs, so
	// we read it from the raw query map rather than the generic iamHandler wrapper.
	"CreateAccessKey": iamAccessKeyHandler(func(accountID string, input *iam.CreateAccessKeyInput, expiresAt string, svc handlers_iam.IAMService) (any, error) {
		return gateway_iam.CreateAccessKey(accountID, input, expiresAt, svc)
	}),
	"ListAccessKeys": iamHandler(func(accountID string, input *iam.ListAccessKeysInput, svc handlers_iam.IAMService) (any, error) {
		return gateway_iam.ListAccessKeys(accountID, input, svc)
	}),
	"DeleteAccessKey": iamHandler(func(accountID string, input *iam.DeleteAccessKeyInput, svc handlers_iam.IAMService) (any, error) {
		return gateway_iam.DeleteAccessKey(accountID, input, svc)
	}),
	"UpdateAccessKey": iamAccessKeyHandler(func(accountID string, input *iam.UpdateAccessKeyInput, expiresAt string, svc handlers_iam.IAMService) (any, error) {
		return gateway_iam.UpdateAccessKey(accountID, input, expiresAt, svc)
	}),

	// Policy CRUD
	"CreatePolicy": iamHandler(func(accountID string, input *iam.CreatePolicyInput, svc handlers_iam.IAMService) (any, error) {
		return gateway_iam.CreatePolicy(accountID, input, svc)
	}),
	"GetPolicy": iamHandler(func(accountID string, input *iam.GetPolicyInput, svc handlers_iam.IAMService) (any, error) {
		return gateway_iam.GetPolicy(accountID, input, svc)
	}),
	"GetPolicyVersion": iamHandler(func(accountID string, input *iam.GetPolicyVersionInput, svc handlers_iam.IAMService) (any, error) {
		return gateway_iam.GetPolicyVersion(accountID, input, svc)
	}),
	"ListPolicies": iamHandler(func(accountID string, input *iam.ListPoliciesInput, svc handlers_iam.IAMService) (any, error) {
		return gateway_iam.ListPolicies(accountID, input, svc)
	}),
	"DeletePolicy": iamHandler(func(accountID string, input *iam.DeletePolicyInput, svc handlers_iam.IAMService) (any, error) {
		return gateway_iam.DeletePolicy(accountID, input, svc)
	}),

	// Policy attachment
	"AttachUserPolicy": iamHandler(func(accountID string, input *iam.AttachUserPolicyInput, svc handlers_iam.IAMService) (any, error) {
		return gateway_iam.AttachUserPolicy(accountID, input, svc)
	}),
	"DetachUserPolicy": iamHandler(func(accountID string, input *iam.DetachUserPolicyInput, svc handlers_iam.IAMService) (any, error) {
		return gateway_iam.DetachUserPolicy(accountID, input, svc)
	}),
	"ListAttachedUserPolicies": iamHandler(func(accountID string, input *iam.ListAttachedUserPoliciesInput, svc handlers_iam.IAMService) (any, error) {
		return gateway_iam.ListAttachedUserPolicies(accountID, input, svc)
	}),
}

func (gw *GatewayConfig) IAM_Request(w http.ResponseWriter, r *http.Request) error {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Error("Failed to read IAM request body", "error", err)
		return errors.New(awserrors.ErrorInternalError)
	}
	queryArgs := ParseAWSQueryArgs(string(body))

	action := queryArgs["Action"]
	handler, ok := iamActions[action]
	if !ok {
		slog.Debug("IAM: unknown action", "action", action)
		return errors.New(awserrors.ErrorInvalidAction)
	}

	if gw.IAMService == nil {
		slog.Error("IAM: service not initialized")
		return errors.New(awserrors.ErrorInternalError)
	}

	if err := gw.checkPolicy(r, "iam", action); err != nil {
		return err
	}

	// Extract account ID from auth context
	accountID, _ := r.Context().Value(ctxAccountID).(string)
	if accountID == "" {
		slog.Error("IAM_Request: no account ID in auth context")
		return errors.New(awserrors.ErrorInternalError)
	}

	xmlOutput, err := handler(action, queryArgs, gw, accountID)
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", "text/xml")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(xmlOutput); err != nil {
		slog.Error("Failed to write IAM response", "err", err)
	}
	return nil
}
