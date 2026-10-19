import 'axios';

declare module 'axios' {
  export interface AxiosRequestConfig {
    disableDuplicate?: boolean;
    skipErrorHandler?: boolean;
  }
}
