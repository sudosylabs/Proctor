import {access, readFile} from 'node:fs/promises';
import {dirname, resolve} from 'node:path';
import {fileURLToPath, pathToFileURL} from 'node:url';

const siteRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const repositoryRoot = resolve(siteRoot, '../..');
const allowedStates = new Set(['documented', 'planned']);

export const EXPECTED_WORKFLOW_IDS = Object.freeze([
  'web-account-entry-and-recovery',
  'account-methods-and-session-security',
  'access-policy-administration',
  'desktop-authorization-and-trust',
  'institution-and-academic-structure',
  'invitations-and-membership',
  'onboarding-imports-and-progression',
  'roles-bindings-and-audit',
  'exam-authoring-and-publication',
  'sitting-lifecycle-and-corrections',
  'candidate-attempt-and-submission',
  'integrity-review-and-result-release',
  'durable-job-and-mail-recovery',
]);

export function auditDocumentationCoverage(
  openapi,
  coverage,
  existingPages,
  expectedWorkflowIDs = EXPECTED_WORKFLOW_IDS,
) {
  const errors = [];
  const tags = new Set((openapi.tags ?? []).map(({name}) => name));
  const operationIDs = new Set();
  for (const pathItem of Object.values(openapi.paths ?? {})) {
    for (const operation of Object.values(pathItem ?? {})) {
      if (operation && typeof operation === 'object' && !Array.isArray(operation) && operation.operationId) {
        operationIDs.add(operation.operationId);
      }
    }
  }

  if (coverage.version !== 1) {
    errors.push('documentation coverage version must be 1');
  }

  const mappedTags = new Set();
  for (const area of coverage.productAreas ?? []) {
    if (!tags.has(area.tag)) {
      errors.push(`product area ${JSON.stringify(area.tag)} is not an OpenAPI tag`);
    }
    if (mappedTags.has(area.tag)) {
      errors.push(`product area ${JSON.stringify(area.tag)} is mapped more than once`);
    }
    mappedTags.add(area.tag);
    if (!existingPages.has(area.page)) {
      errors.push(`product area ${JSON.stringify(area.tag)} targets missing page ${JSON.stringify(area.page)}`);
    }
  }
  for (const tag of tags) {
    if (!mappedTags.has(tag)) {
      errors.push(`OpenAPI product area ${JSON.stringify(tag)} has no authored entry page`);
    }
  }

  const workflowIDs = new Set();
  for (const workflow of coverage.workflows ?? []) {
    if (!workflow.id || workflowIDs.has(workflow.id)) {
      errors.push(`workflow id ${JSON.stringify(workflow.id)} is empty or duplicated`);
    }
    workflowIDs.add(workflow.id);
    if (!Number.isInteger(workflow.phase) || workflow.phase < 1) {
      errors.push(`workflow ${JSON.stringify(workflow.id)} has an invalid phase`);
    }
    if (!allowedStates.has(workflow.state)) {
      errors.push(`workflow ${JSON.stringify(workflow.id)} has invalid state ${JSON.stringify(workflow.state)}`);
    }
    if (!existingPages.has(workflow.page)) {
      errors.push(`workflow ${JSON.stringify(workflow.id)} targets missing page ${JSON.stringify(workflow.page)}`);
    }
    if (!Array.isArray(workflow.operations) || workflow.operations.length < 2) {
      errors.push(`workflow ${JSON.stringify(workflow.id)} must name at least two operations`);
      continue;
    }
    const seen = new Set();
    for (const operationID of workflow.operations) {
      if (!operationIDs.has(operationID)) {
        errors.push(`workflow ${JSON.stringify(workflow.id)} names unknown operation ${JSON.stringify(operationID)}`);
      }
      if (seen.has(operationID)) {
        errors.push(`workflow ${JSON.stringify(workflow.id)} repeats operation ${JSON.stringify(operationID)}`);
      }
      seen.add(operationID);
    }
  }

  const expectedWorkflows = new Set(expectedWorkflowIDs);
  if (expectedWorkflows.size !== expectedWorkflowIDs.length) {
    errors.push('expected workflow inventory contains a duplicate id');
  }
  for (const id of expectedWorkflows) {
    if (!workflowIDs.has(id)) {
      errors.push(`required multi-operation workflow ${JSON.stringify(id)} is missing`);
    }
  }
  for (const id of workflowIDs) {
    if (!expectedWorkflows.has(id)) {
      errors.push(`workflow ${JSON.stringify(id)} is absent from the expected workflow inventory`);
    }
  }

  return errors;
}

async function main() {
  const openapi = JSON.parse(await readFile(resolve(repositoryRoot, 'server/openapi.json'), 'utf8'));
  const coverage = JSON.parse(await readFile(resolve(siteRoot, 'documentation-coverage.json'), 'utf8'));
  const pagePaths = new Set([
    ...(coverage.productAreas ?? []).map(({page}) => page),
    ...(coverage.workflows ?? []).map(({page}) => page),
  ]);
  const existingPages = new Set();
  for (const page of pagePaths) {
    try {
      await access(resolve(repositoryRoot, page));
      existingPages.add(page);
    } catch {
      // The audit reports the missing target with its owning map entry.
    }
  }

  const errors = auditDocumentationCoverage(openapi, coverage, existingPages);
  if (errors.length > 0) {
    console.error(errors.join('\n'));
    process.exitCode = 1;
    return;
  }
  console.log(`Documentation coverage maps ${openapi.tags.length} product areas and ${coverage.workflows.length} multi-operation workflows`);
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  await main();
}
