export function insertApiContractPanel(source, name = 'generated endpoint') {
  if (source.includes('<ApiContractPanel />')) {
    return source;
  }

  if (!/\n<ParamsDetails\b/.test(source)) {
    throw new Error(`${name}: could not locate the generated request-details seam`);
  }

  return source.replace(
    /\n<ParamsDetails\b/,
    '\n<ApiContractPanel />\n\n<ParamsDetails',
  );
}
