import {auditAssetRegistry} from './asset-registry.mjs';

const result = await auditAssetRegistry();

if (result.failures.length > 0) {
  console.error(result.failures.join('\n'));
  process.exitCode = 1;
} else {
  const {assets, files, references} = result.counts;
  console.log(`Governed assets are valid: ${assets} registered, ${files} present, ${references} referenced`);
}
