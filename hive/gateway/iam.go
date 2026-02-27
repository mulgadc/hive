package gateway

import (
	"errors"
	"log/slog"

	"github.com/aws/aws-sdk-go/service/iam"
	"github.com/gofiber/fiber/v2"
	"github.com/mulgadc/hive/hive/awsec2query"
	"github.com/mulgadc/hive/hive/awserrors"
	gateway_iam "github.com/mulgadc/hive/hive/gateway/iam"
	handlers_iam "github.com/mulgadc/hive/hive/handlers/iam"
	"github.com/mulgadc/hive/hive/utils"
)

// IAMHandler processes parsed query args and returns XML response bytes.
type IAMHandler func(action string, q map[string]string, gw *GatewayConfig) ([]byte, error)

// iamHandler creates a type-safe IAMHandler that allocates the typed input struct,
// parses query params into it, calls the handler, and marshals the output to XML.
func iamHandler[In any](handler func(*In, handlers_iam.IAMService) (any, error)) IAMHandler {
	return func(action string, q map[string]string, gw *GatewayConfig) ([]byte, error) {
		input := new(In)
		if err := awsec2query.QueryParamsToStruct(q, input); err != nil {
			return nil, errors.New(awserrors.ErrorIAMInvalidInput)
		}
		output, err := handler(input, gw.IAMService)
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
	"CreateUser": iamHandler(func(input *iam.CreateUserInput, svc handlers_iam.IAMService) (any, error) {
		return gateway_iam.CreateUser(input, svc)
	}),
	"GetUser": iamHandler(func(input *iam.GetUserInput, svc handlers_iam.IAMService) (any, error) {
		return gateway_iam.GetUser(input, svc)
	}),
	"ListUsers": iamHandler(func(input *iam.ListUsersInput, svc handlers_iam.IAMService) (any, error) {
		return gateway_iam.ListUsers(input, svc)
	}),
	"DeleteUser": iamHandler(func(input *iam.DeleteUserInput, svc handlers_iam.IAMService) (any, error) {
		return gateway_iam.DeleteUser(input, svc)
	}),
	"CreateAccessKey": iamHandler(func(input *iam.CreateAccessKeyInput, svc handlers_iam.IAMService) (any, error) {
		return gateway_iam.CreateAccessKey(input, svc)
	}),
	"ListAccessKeys": iamHandler(func(input *iam.ListAccessKeysInput, svc handlers_iam.IAMService) (any, error) {
		return gateway_iam.ListAccessKeys(input, svc)
	}),
	"DeleteAccessKey": iamHandler(func(input *iam.DeleteAccessKeyInput, svc handlers_iam.IAMService) (any, error) {
		return gateway_iam.DeleteAccessKey(input, svc)
	}),
	"UpdateAccessKey": iamHandler(func(input *iam.UpdateAccessKeyInput, svc handlers_iam.IAMService) (any, error) {
		return gateway_iam.UpdateAccessKey(input, svc)
	}),

	// Policy CRUD
	"CreatePolicy": iamHandler(func(input *iam.CreatePolicyInput, svc handlers_iam.IAMService) (any, error) {
		return gateway_iam.CreatePolicy(input, svc)
	}),
	"GetPolicy": iamHandler(func(input *iam.GetPolicyInput, svc handlers_iam.IAMService) (any, error) {
		return gateway_iam.GetPolicy(input, svc)
	}),
	"GetPolicyVersion": iamHandler(func(input *iam.GetPolicyVersionInput, svc handlers_iam.IAMService) (any, error) {
		return gateway_iam.GetPolicyVersion(input, svc)
	}),
	"ListPolicies": iamHandler(func(input *iam.ListPoliciesInput, svc handlers_iam.IAMService) (any, error) {
		return gateway_iam.ListPolicies(input, svc)
	}),
	"DeletePolicy": iamHandler(func(input *iam.DeletePolicyInput, svc handlers_iam.IAMService) (any, error) {
		return gateway_iam.DeletePolicy(input, svc)
	}),

	// Policy attachment
	"AttachUserPolicy": iamHandler(func(input *iam.AttachUserPolicyInput, svc handlers_iam.IAMService) (any, error) {
		return gateway_iam.AttachUserPolicy(input, svc)
	}),
	"DetachUserPolicy": iamHandler(func(input *iam.DetachUserPolicyInput, svc handlers_iam.IAMService) (any, error) {
		return gateway_iam.DetachUserPolicy(input, svc)
	}),
	"ListAttachedUserPolicies": iamHandler(func(input *iam.ListAttachedUserPoliciesInput, svc handlers_iam.IAMService) (any, error) {
		return gateway_iam.ListAttachedUserPolicies(input, svc)
	}),
}

func (gw *GatewayConfig) IAM_Request(ctx *fiber.Ctx) error {
	queryArgs := ParseAWSQueryArgs(string(ctx.Body()))

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

	if err := gw.checkPolicy(ctx, "iam", action); err != nil {
		return err
	}

	xmlOutput, err := handler(action, queryArgs, gw)
	if err != nil {
		return err
	}

	return ctx.Status(fiber.StatusOK).Type("text/xml").Send(xmlOutput)
}
