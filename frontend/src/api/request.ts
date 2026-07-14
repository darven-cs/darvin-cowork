import axios from 'axios';
import type { AxiosInstance, InternalAxiosRequestConfig, AxiosResponse } from 'axios';
const baseURL = import.meta.env.VITE_API_BASE_URL || 'http://127.0.0.1:8080';

const service: AxiosInstance = axios.create({
  baseURL: baseURL,
  timeout: 15000,
  headers: {
    'Content-Type': 'application/json;charset=utf-8',
  },
});

// 请求拦截器
service.interceptors.request.use(
  (config: InternalAxiosRequestConfig) => {
    // 携带token示例
    // const token = localStorage.getItem('token')
    // if (token && config.headers) {
    //   config.headers.Authorization = `Bearer ${token}`
    // }
    return config;
  },
  (error) => {
    return Promise.reject(error);
  },
);

// 响应拦截器
service.interceptors.response.use(
  (response: AxiosResponse) => {
    // 只返回后端内层data，简化页面接收层级
    return response.data;
  },
  (error) => {
    const msg = error.response?.data?.msg || '网络请求失败';
    console.error('请求错误：', msg);
    return Promise.reject(error);
  },
);

export default service;
