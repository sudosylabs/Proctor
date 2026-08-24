export function insertApiContractPanel(source, name = 'generated endpoint') {
  if (source.includes('<ApiContractPanel />')) {
    return source;
  }

  const imports = source.match(/(\n---\n\n)((?:import [^\n]+;\n)+)\n/);
  if (!imports) {
    throw new Error(`${name}: could not locate the generated MDX import block`);
  }

  return source.replace(
    imports[0],
    `${imports[1]}${imports[2]}\n<ApiContractPanel />\n\n`,
  );
}
