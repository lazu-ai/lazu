declare module 'marked' {
  export const marked: {
    parse: (markdown: string) => string;
  };
}
