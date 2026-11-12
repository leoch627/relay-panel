import { readFile } from "node:fs/promises";
import ts from "typescript";

// Exercise the same standalone TypeScript functions used by the UI without
// introducing a second compiler configuration or additional runtime packages.
export async function loadTS(relativePath) {
  const source = await readFile(new URL(relativePath, import.meta.url), "utf8");
  const { outputText } = ts.transpileModule(source, {
    compilerOptions: { target: ts.ScriptTarget.ES2020, module: ts.ModuleKind.ESNext },
  });
  return import(`data:text/javascript;base64,${Buffer.from(outputText).toString("base64")}`);
}
