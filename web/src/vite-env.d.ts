/**
 * Vite's ambient types.
 *
 * What this actually provides is the declaration for importing a .css file for
 * its side effect — `import './styles/app.css'` has no types of its own, and
 * without this TypeScript rejects it with TS2882. The rest of the reference
 * (import.meta.env, the asset modules) comes along and is not used yet.
 */
/// <reference types="vite/client" />
