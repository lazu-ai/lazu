import { useRef } from 'react';
export function useOperationId() {
  const operation = useRef<{ signature: string; id: string }>();
  return {
    forInput(signature: string) {
      if (operation.current?.signature !== signature)
        operation.current = { signature, id: crypto.randomUUID() };
      return operation.current.id;
    },
    completed() {
      operation.current = undefined;
    },
  };
}
