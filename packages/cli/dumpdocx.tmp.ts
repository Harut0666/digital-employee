import mammoth from "mammoth";
import { readFileSync } from "node:fs";
const path = process.argv[2]!;
console.log((await mammoth.extractRawText({ path })).value);
