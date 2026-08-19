// Electrobun ships its API as TypeScript source rather than compiled types, so
// importing it typechecks its whole surface — including optional 3D bindings
// this app will never touch. `skipLibCheck` does not help: these are .ts files,
// not declarations.
//
// Declaring the module as untyped is the smallest honest fix. Installing
// @types/three would add real dependencies to satisfy an import that is never
// evaluated.
declare module "three";
