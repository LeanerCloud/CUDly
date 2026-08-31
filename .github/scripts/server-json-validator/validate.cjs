const fs = require("fs");
const Ajv = require("ajv");
const addFormats = require("ajv-formats");

const [schemaPath, dataPath] = process.argv.slice(2);
if (!schemaPath || !dataPath) {
  console.error("usage: node .github/scripts/server-json-validator/validate.cjs <schema.json> <server.json>");
  process.exit(2);
}

const schema = JSON.parse(fs.readFileSync(schemaPath, "utf8"));
const data = JSON.parse(fs.readFileSync(dataPath, "utf8"));
const ajv = new Ajv({ strict: false, allErrors: true });
addFormats(ajv);

const valid = ajv.validate(schema, data);
if (!valid) {
  console.error(JSON.stringify(ajv.errors, null, 2));
  process.exit(1);
}

console.log(`${dataPath} valid`);
