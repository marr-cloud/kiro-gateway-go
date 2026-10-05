// El runtime de hooks aporta setTimeout; tsconfig no carga lib dom ni @types/node.
declare function setTimeout(handler: (...args: any[]) => void, ms?: number): unknown
