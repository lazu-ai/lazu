import { useQueryClient } from '@tanstack/react-query';
import React, { type ReactNode } from 'react';
import {
  reducer,
  initialState,
  type UserAction,
  type UserState,
} from './reducer';
import { queryKeys } from '../../lib/query-keys';

export type UserContextValue = [UserState, React.Dispatch<UserAction>];

const noopDispatch: React.Dispatch<UserAction> = () => undefined;

export const UserContext = React.createContext<UserContextValue>([
  initialState,
  noopDispatch,
]);

type UserProviderProps = {
  children: ReactNode;
};

export const UserProvider = ({ children }: UserProviderProps) => {
  const [state, reduce] = React.useReducer(reducer, initialState);
  const queryClient = useQueryClient();
  const dispatch = React.useCallback<React.Dispatch<UserAction>>(
    (action) => {
      if (action.type === 'logout') {
        // A new session must not reuse the old session's memberships or flags,
        // even when the same account signs in within the freshness window.
        // Removal also cancels an in-flight self read before it can refill cache.
        queryClient.removeQueries({ queryKey: [...queryKeys.user, 'self'] });
      }
      reduce(action);
    },
    [queryClient],
  );

  return (
    <UserContext.Provider value={[state, dispatch]}>
      {children}
    </UserContext.Provider>
  );
};
