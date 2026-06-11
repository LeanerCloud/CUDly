// Type declarations for @ungap/structured-clone, which ships no .d.ts files.
// Used only by the Jest setup (setup.ts) to polyfill structuredClone with
// faithful HTML structured clone semantics under jest-environment-jsdom.
declare module '@ungap/structured-clone' {
  interface StructuredCloneOptions {
    transfer?: Transferable[];
    json?: boolean;
    lossy?: boolean;
  }
  const structuredClonePolyfill: <T>(value: T, options?: StructuredCloneOptions) => T;
  export default structuredClonePolyfill;
}
