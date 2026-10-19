import React, { type ReactNode } from 'react';
import {
  initialState,
  reducer,
  type StatusAction,
  type StatusState,
} from './reducer';

export type StatusContextValue = [StatusState, React.Dispatch<StatusAction>];

const noopDispatch: React.Dispatch<StatusAction> = () => undefined;

export const StatusContext = React.createContext<StatusContextValue>([
  initialState,
  noopDispatch,
]);

type StatusProviderProps = {
  children: ReactNode;
};

export const StatusProvider = ({ children }: StatusProviderProps) => {
  const [state, dispatch] = React.useReducer(reducer, initialState);

  return (
    <StatusContext.Provider value={[state, dispatch]}>
      {children}
    </StatusContext.Provider>
  );
};
