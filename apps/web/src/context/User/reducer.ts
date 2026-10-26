export type UserPayload =
  | (Record<string, unknown> & {
      display_name?: string;
      group?: string;
      id?: number | string;
      role?: number | string;
      username?: string;
    })
  | undefined;

export type UserState = {
  user?: UserPayload;
};

export type UserAction =
  | {
      type: 'login';
      payload: UserPayload;
    }
  | {
      type: 'logout';
    }
  | {
      type: string;
      payload?: UserPayload;
    };
