import { afterEach, describe, expect, it, vi } from "vitest"

const mockSend = vi.fn().mockResolvedValue({})

vi.mock("@/lib/awsClient", () => ({
  getS3Client: () => ({ send: mockSend }),
}))

import { s3BucketObjectsQueryOptions, s3BucketsQueryOptions } from "./s3"

describe("query keys", () => {
  it("s3BucketsQueryOptions has correct key", () => {
    expect(s3BucketsQueryOptions.queryKey).toEqual(["s3", "buckets"])
  })

  it("s3BucketObjectsQueryOptions includes bucket and empty prefix in key", () => {
    expect(s3BucketObjectsQueryOptions("my-bucket").queryKey).toEqual([
      "s3",
      "buckets",
      "my-bucket",
      "objects",
      "",
    ])
  })

  it("s3BucketObjectsQueryOptions includes prefix in key", () => {
    expect(
      s3BucketObjectsQueryOptions("my-bucket", "photos/").queryKey,
    ).toEqual(["s3", "buckets", "my-bucket", "objects", "photos/"])
  })
})

describe("queryFn", () => {
  afterEach(() => {
    mockSend.mockClear()
  })

  it("s3BucketsQueryOptions sends ListBucketsCommand", async () => {
    const queryFn = s3BucketsQueryOptions.queryFn as (
      ctx: never,
    ) => Promise<unknown>
    await queryFn({} as never)
    expect(mockSend).toHaveBeenCalledOnce()
    expect(mockSend.mock.calls[0]?.[0].input).toEqual({})
  })

  it("s3BucketObjectsQueryOptions sends ListObjectsV2Command with bucket and delimiter", async () => {
    const queryFn = s3BucketObjectsQueryOptions("my-bucket").queryFn as (
      ctx: never,
    ) => Promise<unknown>
    await queryFn({} as never)
    expect(mockSend.mock.calls[0]?.[0].input).toEqual({
      Bucket: "my-bucket",
      Prefix: undefined,
      Delimiter: "/",
    })
  })

  it("s3BucketObjectsQueryOptions sends prefix when provided", async () => {
    const queryFn = s3BucketObjectsQueryOptions("my-bucket", "docs/")
      .queryFn as (ctx: never) => Promise<unknown>
    await queryFn({} as never)
    expect(mockSend.mock.calls[0]?.[0].input).toEqual({
      Bucket: "my-bucket",
      Prefix: "docs/",
      Delimiter: "/",
    })
  })
})
