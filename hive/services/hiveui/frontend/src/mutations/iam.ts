import {
  AttachUserPolicyCommand,
  CreateAccessKeyCommand,
  CreatePolicyCommand,
  CreateUserCommand,
  DeleteAccessKeyCommand,
  DeletePolicyCommand,
  DeleteUserCommand,
  DetachUserPolicyCommand,
  type StatusType,
  UpdateAccessKeyCommand,
} from "@aws-sdk/client-iam"
import { useMutation, useQueryClient } from "@tanstack/react-query"

import { getIamClient } from "@/lib/awsClient"
import {
  iamAccessKeysQueryOptions,
  iamAttachedUserPoliciesQueryOptions,
  iamPoliciesQueryOptions,
  iamUsersQueryOptions,
} from "@/queries/iam"
import type { CreatePolicyFormData, CreateUserFormData } from "@/types/iam"

export function useCreateUser() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (params: CreateUserFormData) => {
      const command = new CreateUserCommand({
        UserName: params.userName,
        Path: params.path || undefined,
      })
      return getIamClient().send(command)
    },
    onSuccess: () => {
      queryClient.invalidateQueries(iamUsersQueryOptions)
    },
  })
}

export function useDeleteUser() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (userName: string) => {
      const command = new DeleteUserCommand({ UserName: userName })
      return getIamClient().send(command)
    },
    onSuccess: () => {
      queryClient.invalidateQueries(iamUsersQueryOptions)
    },
  })
}

export function useCreateAccessKey() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (userName: string) => {
      const command = new CreateAccessKeyCommand({ UserName: userName })
      return getIamClient().send(command)
    },
    onSuccess: (_data, userName) => {
      queryClient.invalidateQueries(iamAccessKeysQueryOptions(userName))
    },
  })
}

export function useDeleteAccessKey() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({
      userName,
      accessKeyId,
    }: {
      userName: string
      accessKeyId: string
    }) => {
      const command = new DeleteAccessKeyCommand({
        UserName: userName,
        AccessKeyId: accessKeyId,
      })
      return getIamClient().send(command)
    },
    onSuccess: (_data, { userName }) => {
      queryClient.invalidateQueries(iamAccessKeysQueryOptions(userName))
    },
  })
}

export function useUpdateAccessKey() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({
      userName,
      accessKeyId,
      status,
    }: {
      userName: string
      accessKeyId: string
      status: StatusType
    }) => {
      const command = new UpdateAccessKeyCommand({
        UserName: userName,
        AccessKeyId: accessKeyId,
        Status: status,
      })
      return getIamClient().send(command)
    },
    onSuccess: (_data, { userName }) => {
      queryClient.invalidateQueries(iamAccessKeysQueryOptions(userName))
    },
  })
}

export function useCreatePolicy() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (params: CreatePolicyFormData) => {
      const command = new CreatePolicyCommand({
        PolicyName: params.policyName,
        Description: params.description || undefined,
        PolicyDocument: params.policyDocument,
      })
      return getIamClient().send(command)
    },
    onSuccess: () => {
      queryClient.invalidateQueries(iamPoliciesQueryOptions)
    },
  })
}

export function useDeletePolicy() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (policyArn: string) => {
      const command = new DeletePolicyCommand({ PolicyArn: policyArn })
      return getIamClient().send(command)
    },
    onSuccess: () => {
      queryClient.invalidateQueries(iamPoliciesQueryOptions)
    },
  })
}

export function useAttachUserPolicy() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({
      userName,
      policyArn,
    }: {
      userName: string
      policyArn: string
    }) => {
      const command = new AttachUserPolicyCommand({
        UserName: userName,
        PolicyArn: policyArn,
      })
      return getIamClient().send(command)
    },
    onSuccess: (_data, { userName }) => {
      queryClient.invalidateQueries(
        iamAttachedUserPoliciesQueryOptions(userName),
      )
    },
  })
}

export function useDetachUserPolicy() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({
      userName,
      policyArn,
    }: {
      userName: string
      policyArn: string
    }) => {
      const command = new DetachUserPolicyCommand({
        UserName: userName,
        PolicyArn: policyArn,
      })
      return getIamClient().send(command)
    },
    onSuccess: (_data, { userName }) => {
      queryClient.invalidateQueries(
        iamAttachedUserPoliciesQueryOptions(userName),
      )
    },
  })
}
