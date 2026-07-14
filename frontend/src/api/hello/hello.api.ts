import service from '../request';
import type { HelloReq, HelloResp } from './hello.types';

// 与后端 backend/cmd/main.go 中 http.HandleFunc("/api/hello", ...) 对应
// baseURL 已是 http://127.0.0.1:8080,所以这里路径写 /api/hello
export function sayHello(params?: HelloReq): Promise<HelloResp> {
  return service.get('/api/hello', { params });
}
