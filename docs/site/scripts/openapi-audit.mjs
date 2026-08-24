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

const mutationMethods = new Set(['delete', 'patch', 'post', 'put']);
const idempotencyRequirements = new Set(['none', 'optional', 'required']);
const minimumBehaviorDescriptionLength = 80;
const minimumTagDescriptionLength = 40;

// Keep exceptions precise and reviewed. Phase 2 currently needs none.
export const parameterDescriptionAllowlist = Object.freeze([]);
const allowedMissingParameterDescriptions = new Set(parameterDescriptionAllowlist);

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

function hasCodeSample(operation) {
  return (
    Array.isArray(operation?.['x-codeSamples']) &&
    operation['x-codeSamples'].some(
      (sample) => isNonemptyString(sample?.lang) && isNonemptyString(sample?.source),
    )
  );
}

function hasMediaExample(mediaType) {
  return mediaType?.example !== undefined || Object.keys(mediaType?.examples ?? {}).length > 0;
}

function hasContentExample(content) {
  return Object.values(content ?? {}).some(hasMediaExample);
}

function parameterPolicyKey(method, path, parameter) {
  return `${method} ${path} ${parameter?.in ?? 'unknown'}:${parameter?.name ?? 'unknown'}`;
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
    declaredTags.set(tag.name, {
      description: tag.description,
      hasProblemExample: false,
      hasSuccessExample: false,
      operations: 0,
      requiresProblemExample: false,
      requiresSuccessExample: false,
    });
    if (
      !isNonemptyString(tag.description) ||
      tag.description.trim().length < minimumTagDescriptionLength
    ) {
      errors.push(
        `tag ${JSON.stringify(tag.name)}: description must contain at least ${minimumTagDescriptionLength} characters`,
      );
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
  let parameters = 0;
  let parameterDescriptions = 0;
  let requestBodies = 0;
  let requestBodyDescriptions = 0;
  let mutations = 0;
  let mutationExamples = 0;

  for (const entry of operations) {
    const {method, operation, path, pathItem} = entry;
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
    if (
      isNonemptyString(operation?.description) &&
      operation.description.trim().length >= minimumBehaviorDescriptionLength
    ) {
      descriptions += 1;
    } else {
      errors.push(
        `${location}: behavior description must contain at least ${minimumBehaviorDescriptionLength} characters`,
      );
    }

    let tag;
    if (Array.isArray(operation?.tags) && operation.tags.length === 1) {
      tagged += 1;
      tag = declaredTags.get(operation.tags[0]);
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

    const codeSample = hasCodeSample(operation);
    if (codeSample) {
      codeSamples += 1;
    }

    for (const parameterReference of [
      ...(pathItem.parameters ?? []),
      ...(operation.parameters ?? []),
    ]) {
      parameters += 1;
      const parameter = resolveReference(document, parameterReference);
      const label = parameter?.name ?? parameterReference?.$ref ?? 'unknown parameter';
      if (!parameter) {
        errors.push(`${location}: ${label}: parameter reference cannot be resolved`);
        continue;
      }
      const policyKey = parameterPolicyKey(method, path, parameter);
      if (
        isNonemptyString(parameter.description) ||
        allowedMissingParameterDescriptions.has(policyKey)
      ) {
        parameterDescriptions += 1;
      } else {
        errors.push(`${location}: ${label}: parameter description is required`);
      }
    }

    let requestBody;
    if (operation.requestBody) {
      requestBodies += 1;
      requestBody = resolveReference(document, operation.requestBody);
      if (!requestBody) {
        errors.push(`${location}: request body reference cannot be resolved`);
      } else if (isNonemptyString(requestBody.description)) {
        requestBodyDescriptions += 1;
      } else {
        errors.push(`${location}: request body description is required`);
      }
    }

    if (mutationMethods.has(method.toLowerCase())) {
      mutations += 1;
      if (codeSample || hasContentExample(requestBody?.content)) {
        mutationExamples += 1;
      } else {
        errors.push(`${location}: mutation request example is required`);
      }
    }

    for (const [status, responseReference] of Object.entries(operation.responses ?? {})) {
      const response = resolveReference(document, responseReference);
      if (!response || !tag) {
        continue;
      }
      if (/^2\d\d$/.test(status) && Object.keys(response.content ?? {}).length > 0) {
        tag.requiresSuccessExample = true;
        if (hasContentExample(response.content)) {
          tag.hasSuccessExample = true;
        }
      }
      const problem = response.content?.['application/problem+json'];
      if (problem) {
        tag.requiresProblemExample = true;
        if (hasMediaExample(problem)) {
          tag.hasProblemExample = true;
        }
      }
    }
  }

  let tagSuccessExamples = 0;
  let tagsRequiringSuccessExamples = 0;
  let tagProblemExamples = 0;
  let tagsRequiringProblemExamples = 0;
  for (const [name, tag] of declaredTags) {
    if (tag.operations === 0) {
      errors.push(`tag ${JSON.stringify(name)}: at least one operation is required`);
    }
    if (tag.requiresSuccessExample) {
      tagsRequiringSuccessExamples += 1;
      if (tag.hasSuccessExample) {
        tagSuccessExamples += 1;
      } else {
        errors.push(
          `tag ${JSON.stringify(name)}: at least one representative success response example is required`,
        );
      }
    }
    if (tag.requiresProblemExample) {
      tagsRequiringProblemExamples += 1;
      if (tag.hasProblemExample) {
        tagProblemExamples += 1;
      } else {
        errors.push(
          `tag ${JSON.stringify(name)}: at least one representative Problem Details example is required`,
        );
      }
    }
  }

  const total = operations.length;
  return {
    ok: errors.length === 0,
    version: 2,
    totals: {
      mutations,
      operations: total,
      parameters,
      paths: Object.keys(document.paths ?? {}).length,
      requestBodies,
      schemas: Object.keys(document.components?.schemas ?? {}).length,
      tags: declaredTags.size,
    },
    coverage: {
      codeSamples: coverage(codeSamples, total),
      descriptions: coverage(descriptions, total),
      explicitAuth: coverage(explicitAuth, total),
      explicitErrorCodes: coverage(explicitErrorCodes, total),
      explicitIdempotency: coverage(explicitIdempotency, total),
      mutationExamples: coverage(mutationExamples, mutations),
      parameterDescriptions: coverage(parameterDescriptions, parameters),
      requestBodyDescriptions: coverage(requestBodyDescriptions, requestBodies),
      summaries: coverage(summaries, total),
      tagged: coverage(tagged, total),
      tagProblemExamples: coverage(tagProblemExamples, tagsRequiringProblemExamples),
      tagSuccessExamples: coverage(tagSuccessExamples, tagsRequiringSuccessExamples),
    },
    tags: [...declaredTags.entries()].map(([name, value]) => ({
      description: value.description,
      hasProblemExample: value.hasProblemExample,
      hasSuccessExample: value.hasSuccessExample,
      name,
      operations: value.operations,
    })),
    errors,
  };
}
