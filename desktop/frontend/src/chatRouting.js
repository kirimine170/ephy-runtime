export function shouldUseGenericRagEndpoint({mode, webSearchEnabled, sourceScope}) {
  return mode === 'rag'
    && !webSearchEnabled
    && sourceScope !== 'personal_context';
}
