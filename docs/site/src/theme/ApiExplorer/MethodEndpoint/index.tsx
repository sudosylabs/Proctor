type Props = {
  method: string;
  path: string;
  context?: 'endpoint' | 'callback';
};

export default function MethodEndpoint({method, path}: Props): React.JSX.Element {
  const label = method === 'event' ? 'Webhook' : method.toUpperCase();

  return (
    <>
      <div aria-label={`${label} ${path}`} className="openapi__method-endpoint">
        <span className="badge badge--primary">{label}</span>
        {method !== 'event' ? (
          <code className="openapi__method-endpoint-path">{path}</code>
        ) : null}
      </div>
      <div className="openapi__divider" />
    </>
  );
}
