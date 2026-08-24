const operationMethods = new Set([
  'delete',
  'get',
  'head',
  'options',
  'patch',
  'post',
  'put',
  'trace',
]);

const idempotencyRequirements = new Set(['none', 'optional', 'required']);

export const pilotOperationIds = Object.freeze([
  'getPublicAccessDiscovery',
  'login',
  'regenerateMFARecoveryCodes',
  'listAcademicUnits',
  'createRootAcademicUnit',
  'replaceAccessPolicy',
  'uploadUserProfilePicture',
  'getUserProfilePicture',
  'listExams',
  'createExam',
  'publishExamRevision',
  'createCandidateExamWorkspaceFile',
  'getCandidateExamWorkspaceContent',
  'submitExamAttempt',
  'connectWebSocket',
]);

function operationEntries(document) {
  const entries = [];
  for (const [path, pathItem] of Object.entries(document.paths ?? {})) {
    for (const [method, operation] of Object.entries(pathItem ?? {})) {
      if (operationMethods.has(method.toLowerCase())) {
        entries.push({method: method.toUpperCase(), operation, path, pathItem});
      }
    }
  }
  return entries.sort((left, right) =>
    `${left.path} ${left.method}`.localeCompare(`${right.path} ${right.method}`),
  );
}

function percentage(count, total) {
  return total === 0 ? 100 : Number(((count / total) * 100).toFixed(1));
}

function coverage(count, total) {
  return {count, percent: percentage(count, total), total};
}

function resolveReference(document, value) {
  if (!value?.$ref) {
    return value;
  }
  if (!value.$ref.startsWith('#/')) {
    return undefined;
  }
  return value.$ref
    .slice(2)
    .split('/')
    .map((segment) => segment.replaceAll('~1', '/').replaceAll('~0', '~'))
    .reduce((current, segment) => current?.[segment], document);
}

function isNonemptyString(value) {
  return typeof value === 'string' && value.trim().length > 0;
}

export function auditOpenAPI(document) {
  const errors = [];
  const operations = operationEntries(document);
  const operationById = new Map();
  const declaredTags = new Map();

  for (const [index, tag] of (document.tags ?? []).entries()) {
    if (!isNonemptyString(tag?.name)) {
      errors.push(`tags[${index}]: name is required`);
      continue;
    }
    if (declaredTags.has(tag.name)) {
      errors.push(`tags[${index}]: duplicate tag ${JSON.stringify(tag.name)}`);
    }
    declaredTags.set(tag.name, {description: tag.description, operations: 0});
    if (!isNonemptyString(tag.description)) {
      errors.push(`tag ${JSON.stringify(tag.name)}: description is required`);
    }
  }
  if (declaredTags.size === 0) {
    errors.push('tags: at least one top-level tag is required');
  }

  let summaries = 0;
  let descriptions = 0;
  let tagged = 0;
  let codeSamples = 0;
  let explicitAuth = 0;
  let explicitErrorCodes = 0;
  let explicitIdempotency = 0;

  for (const entry of operations) {
    const {method, operation, path} = entry;
    const location = `${method} ${path}`;
    const operationId = operation?.operationId;

    if (!isNonemptyString(operationId)) {
      errors.push(`${location}: operationId is required`);
    } else if (operationById.has(operationId)) {
      errors.push(
        `${location}: operationId ${JSON.stringify(operationId)} is already used by ${operationById.get(operationId).location}`,
      );
    } else {
      operationById.set(operationId, {...entry, location});
    }

    if (isNonemptyString(operation?.summary)) {
      summaries += 1;
    } else {
      errors.push(`${location}: summary is required`);
    }
    if (isNonemptyString(operation?.description)) {
      descriptions += 1;
    }

    if (Array.isArray(operation?.tags) && operation.tags.length === 1) {
      tagged += 1;
      const tag = declaredTags.get(operation.tags[0]);
      if (tag) {
        tag.operations += 1;
      } else {
        errors.push(`${location}: unknown tag ${JSON.stringify(operation.tags[0])}`);
      }
    } else {
      errors.push(`${location}: exactly one tag is required`);
    }

    if (isNonemptyString(operation?.['x-proctor-auth'])) {
      explicitAuth += 1;
    } else {
      errors.push(`${location}: x-proctor-auth is required`);
    }
    if (Array.isArray(operation?.['x-proctor-error-codes'])) {
      explicitErrorCodes += 1;
    } else {
      errors.push(`${location}: x-proctor-error-codes must be an array`);
    }
    if (idempotencyRequirements.has(operation?.['x-proctor-idempotency'])) {
      explicitIdempotency += 1;
    } else {
      errors.push(`${location}: x-proctor-idempotency must be none, optional, or required`);
    }
    if (
      Array.isArray(operation?.['x-codeSamples']) &&
      operation['x-codeSamples'].some(
        (sample) => isNonemptyString(sample?.lang) && isNonemptyString(sample?.source),
      )
    ) {
      codeSamples += 1;
    }
  }

  let completePilotOperations = 0;
  const pilot = [];
  for (const operationId of pilotOperationIds) {
    const entry = operationById.get(operationId);
    const pilotErrors = [];
    if (!entry) {
      pilotErrors.push('operation is missing');
    } else {
      const {operation, pathItem} = entry;
      if (!isNonemptyString(operation.description) || operation.description.trim().length < 80) {
        pilotErrors.push('description must contain at least 80 characters');
      }
      if (
        !Array.isArray(operation['x-codeSamples']) ||
        !operation['x-codeSamples'].some(
          (sample) => isNonemptyString(sample?.lang) && isNonemptyString(sample?.source),
        )
      ) {
        pilotErrors.push('at least one x-codeSamples example is required');
      }

      const parameters = [...(pathItem.parameters ?? []), ...(operation.parameters ?? [])];
      for (const parameterReference of parameters) {
        const parameter = resolveReference(document, parameterReference);
        const label = parameter?.name ?? parameterReference?.$ref ?? 'unknown parameter';
        if (!parameter) {
          pilotErrors.push(`${label}: parameter reference cannot be resolved`);
        } else if (!isNonemptyString(parameter.description)) {
          pilotErrors.push(`${label}: parameter description is required`);
        }
      }

      if (operation.requestBody) {
        const requestBody = resolveReference(document, operation.requestBody);
        if (!requestBody) {
          pilotErrors.push('request body reference cannot be resolved');
        } else if (!isNonemptyString(requestBody.description)) {
          pilotErrors.push('request body description is required');
        }
      }
    }

    if (pilotErrors.length === 0) {
      completePilotOperations += 1;
    } else {
      errors.push(...pilotErrors.map((error) => `${operationId}: ${error}`));
    }
    pilot.push({complete: pilotErrors.length === 0, errors: pilotErrors, operationId});
  }

  const total = operations.length;
  return {
    ok: errors.length === 0,
    version: 1,
    totals: {
      operations: total,
      paths: Object.keys(document.paths ?? {}).length,
      schemas: Object.keys(document.components?.schemas ?? {}).length,
      tags: declaredTags.size,
    },
    coverage: {
      codeSamples: coverage(codeSamples, total),
      descriptions: coverage(descriptions, total),
      explicitAuth: coverage(explicitAuth, total),
      explicitErrorCodes: coverage(explicitErrorCodes, total),
      explicitIdempotency: coverage(explicitIdempotency, total),
      summaries: coverage(summaries, total),
      tagged: coverage(tagged, total),
    },
    tags: [...declaredTags.entries()].map(([name, value]) => ({
      description: value.description,
      name,
      operations: value.operations,
    })),
    pilot: {
      complete: completePilotOperations,
      expected: pilotOperationIds.length,
      operations: pilot,
    },
    errors,
  };
}
