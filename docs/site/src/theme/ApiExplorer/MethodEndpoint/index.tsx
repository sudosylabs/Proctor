import {Fragment} from 'react';

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
          <code className="openapi__method-endpoint-path">
            {path.split('/').map((segment, index) => (
              <Fragment key={index}>
                {index > 0 ? <wbr /> : null}
                {index > 0 ? '/' : ''}{segment}
              </Fragment>
            ))}
          </code>
        ) : null}
      </div>
      <div className="openapi__divider" />
    </>
  );
}
