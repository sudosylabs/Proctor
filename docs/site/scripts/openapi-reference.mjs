const HTTP_METHODS = new Set([
  'get',
  'put',
  'post',
  'delete',
  'options',
  'head',
  'patch',
  'trace',
]);

function operationInventory(specification) {
  const operations = [];

  for (const [path, pathItem] of Object.entries(specification.paths ?? {})) {
    for (const [method, operation] of Object.entries(pathItem ?? {})) {
      if (!HTTP_METHODS.has(method) || typeof operation !== 'object' || !operation) {
        continue;
      }
      operations.push({method, path, operation});
    }
  }

  return operations;
}

function parseGeneratedApi(page) {
  const match = page.source.match(/^api:\s*(\{.*\})\s*$/m);
  if (!match) {
    throw new Error(`${page.name}: missing uncompressed api frontmatter`);
  }

  try {
    return JSON.parse(match[1]);
  } catch (error) {
    throw new Error(`${page.name}: invalid api frontmatter: ${error.message}`);
  }
}

function frontmatterValue(source, field) {
  const match = source.match(new RegExp(`^${field}:\\s*(.+?)\\s*$`, 'm'));
  if (!match) {
    return undefined;
  }
  try {
    return JSON.parse(match[1]);
  } catch {
    return match[1];
  }
}

function sameJson(left, right) {
  return JSON.stringify(left) === JSON.stringify(right);
}

function countExactSidebarId(sidebar, id) {
  const escaped = id.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  return (sidebar.match(new RegExp(`id:\\s*"reference/${escaped}"`, 'g')) ?? [])
    .length;
}

export function verifyOpenapiReference({specification, pages, tagPages, sidebar}) {
  const failures = [];
  const expectedOperations = operationInventory(specification);
  const expectedById = new Map(
    expectedOperations.map(({method, path, operation}) => [
      operation.operationId,
      {method, path, operation},
    ]),
  );
  const generatedById = new Map();

  if (expectedById.size !== expectedOperations.length) {
    failures.push('canonical specification contains missing or duplicate operationId values');
  }

  for (const page of pages) {
    let generated;
    try {
      generated = parseGeneratedApi(page);
    } catch (error) {
      failures.push(error.message);
      continue;
    }

    const operationId = generated.operationId;
    if (!operationId) {
      failures.push(`${page.name}: generated operationId is missing`);
      continue;
    }
    if (generatedById.has(operationId)) {
      failures.push(`${operationId}: generated more than once`);
      continue;
    }
    generatedById.set(operationId, {page, generated});

    const expected = expectedById.get(operationId);
    if (!expected) {
      failures.push(`${operationId}: not present in the canonical specification`);
      continue;
    }

    if (generated.method !== expected.method || generated.path !== expected.path) {
      failures.push(
        `${operationId}: expected ${expected.method.toUpperCase()} ${expected.path}, ` +
          `generated ${String(generated.method).toUpperCase()} ${generated.path}`,
      );
    }
    if (!sameJson(generated.tags, expected.operation.tags)) {
      failures.push(`${operationId}: generated tags differ from the canonical operation`);
    }

    for (const extension of [
      'x-proctor-auth',
      'x-proctor-error-codes',
      'x-proctor-idempotency',
    ]) {
      const expectedValue = expected.operation[extension];
      if (!sameJson(generated[extension], expectedValue)) {
        failures.push(`${operationId}: generated ${extension} differs from the canonical operation`);
      }
      const renderedExtension = generated.extensions?.find(
        (candidate) => candidate.key === extension,
      );
      if (!renderedExtension || !sameJson(renderedExtension.value, expectedValue)) {
        failures.push(`${operationId}: renderer did not preserve ${extension}`);
      }
    }

    if (!page.source.includes('<ApiContractPanel />')) {
      failures.push(`${operationId}: Proctor contract panel is missing`);
    }
    if (!/^hide_send_button:\s*true$/m.test(page.source)) {
      failures.push(`${operationId}: interactive request sending is not disabled`);
    }

    const pageId = frontmatterValue(page.source, 'id');
    if (!pageId || countExactSidebarId(sidebar, pageId) !== 1) {
      failures.push(`${operationId}: generated sidebar entry is missing or duplicated`);
    }
  }

  for (const operationId of expectedById.keys()) {
    if (!generatedById.has(operationId)) {
      failures.push(`${operationId}: generated endpoint page is missing`);
    }
  }

  const apiMethodEntries = sidebar.match(/className:\s*"api-method [a-z]+"/g) ?? [];
  if (apiMethodEntries.length !== expectedOperations.length) {
    failures.push(
      `sidebar contains ${apiMethodEntries.length} endpoint entries; ` +
        `expected ${expectedOperations.length}`,
    );
  }

  const expectedTags = new Set((specification.tags ?? []).map((tag) => tag.name));
  const generatedTags = new Set(
    tagPages.map((page) => frontmatterValue(page.source, 'title')).filter(Boolean),
  );
  for (const tag of expectedTags) {
    if (!generatedTags.has(tag)) {
      failures.push(`${tag}: generated tag page is missing`);
    }
  }
  for (const tag of generatedTags) {
    if (!expectedTags.has(tag)) {
      failures.push(`${tag}: generated tag page is not declared by the specification`);
    }
  }

  if (failures.length > 0) {
    throw new Error(failures.join('\n'));
  }

  return {
    operationCount: expectedOperations.length,
    tagCount: expectedTags.size,
  };
}
