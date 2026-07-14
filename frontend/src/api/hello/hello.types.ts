// hello 业务的请求 / 响应类型
// 与后端 backend/cmd/main.go 的 helloHandler 返回字段保持一致

export interface HelloReq {
  name?: string;
}

export interface HelloResp {
  msg: string;
  from?: string;
  path?: string;
  method?: string;
}
