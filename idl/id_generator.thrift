// ─── ID Generator Thrift IDL (for Kitex) ─────────────────────────────────────
// 生成命令（需安装 kitex 工具）:
//   kitex -type thrift -module github.com/xiongwp/id-generator \
//         -out-dir gen/kitex_gen idl/id_generator.thrift

namespace go idgen.v1

// ─── 错误码 ────────────────────────────────────────────────────────────────────
const i32 CODE_SUCCESS       = 0
const i32 CODE_BIZ_NOT_FOUND = 1001  // biz_tag 未注册
const i32 CODE_SEGMENT_ERROR = 1002  // 号段获取失败
const i32 CODE_OVERFLOW      = 1003  // 号段溢出
const i32 CODE_INVALID_PARAM = 1004  // 参数非法

// ─── 号段模式 ──────────────────────────────────────────────────────────────────

struct GetSegmentIdRequest {
    1: required string biz_tag,
}

struct GetSegmentIdResponse {
    1: required i64    id,
    2: required i32    code,
    3: optional string message,
}

struct BatchGetSegmentIdRequest {
    1: required string biz_tag,
    2: required i32    count,     // 最大 10000
}

struct BatchGetSegmentIdResponse {
    1: required list<i64> ids,
    2: required i32       code,
    3: optional string    message,
}

// ─── 雪花模式 ──────────────────────────────────────────────────────────────────

struct GetSnowflakeIdRequest {}

struct GetSnowflakeIdResponse {
    1: required i64    id,
    2: required i32    code,
    3: optional string message,
}

struct BatchGetSnowflakeIdRequest {
    1: required i32 count,  // 最大 10000
}

struct BatchGetSnowflakeIdResponse {
    1: required list<i64> ids,
    2: required i32       code,
    3: optional string    message,
}

// ─── 管理接口 ──────────────────────────────────────────────────────────────────

struct RegisterBizTagRequest {
    1: required string biz_tag,
    2: optional i64    init_id     = 1,
    3: optional i32    step        = 10000,
    4: optional string description,
}

struct RegisterBizTagResponse {
    1: required i32    code,
    2: optional string message,
}

// ─── 服务定义 ──────────────────────────────────────────────────────────────────

service IdGenService {
    GetSegmentIdResponse      GetSegmentId      (1: GetSegmentIdRequest req),
    BatchGetSegmentIdResponse BatchGetSegmentId (1: BatchGetSegmentIdRequest req),
    GetSnowflakeIdResponse    GetSnowflakeId    (1: GetSnowflakeIdRequest req),
    BatchGetSnowflakeIdResponse BatchGetSnowflakeId (1: BatchGetSnowflakeIdRequest req),
    RegisterBizTagResponse    RegisterBizTag    (1: RegisterBizTagRequest req),
}
