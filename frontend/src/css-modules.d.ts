// Allow TypeScript to accept CSS/SCSS side-effect imports (e.g. import './styles.css').
// Without this, strict TypeScript 5.9+ rejects non-JS module imports by default.
// The webpack config handles the actual CSS bundling at build time.
declare module '*.css' {
  const _: string;
  export default _;
}
