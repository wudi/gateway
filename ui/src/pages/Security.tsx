import { useDashboard, useCertificates, useRules } from '../api/hooks';
import { Spinner } from '../components/shared/Spinner';

export function SecurityPage() {
  const { isLoading: dashLoading } = useDashboard();
  const { data: certs, isLoading: certsLoading } = useCertificates();
  const { data: rules, isLoading: rulesLoading } = useRules();

  if (dashLoading || certsLoading || rulesLoading) {
    return <div className="flex justify-center py-12"><Spinner size="lg" /></div>;
  }

  const certData = (certs ?? []) as Array<{ hostname: string; days_remaining: number }>;
  const expiringCerts = certData.filter((c) => c.days_remaining <= 30);
  const rulesData = (rules ?? { request_rules: [], response_rules: [] }) as {
    request_rules: unknown[];
    response_rules: unknown[];
  };

  return (
    <div className="space-y-6">
      <h1 className="text-xl font-semibold text-text-primary">Security</h1>

      <div>
        <h2 className="text-sm font-medium text-text-primary mb-3">WAF</h2>
        <p className="text-sm text-text-secondary">WAF stats: 0 blocked requests</p>
      </div>

      <div>
        <h2 className="text-sm font-medium text-text-primary mb-3">Rules Engine</h2>
        <p className="text-sm text-text-secondary">
          Request rules: {rulesData.request_rules.length} / Response rules: {rulesData.response_rules.length}
        </p>
      </div>

      <div>
        <h2 className="text-sm font-medium text-text-primary mb-3">Auth</h2>
        <p className="text-sm text-text-secondary">Authentication stats available via /stats</p>
      </div>

      {expiringCerts.length > 0 && (
        <div>
          <h2 className="text-sm font-medium text-text-primary mb-3">Certificate Expiry Alerts</h2>
          <ul className="text-sm space-y-1">
            {expiringCerts.map((cert) => (
              <li key={cert.hostname} className="text-amber-400">
                {cert.hostname} — {cert.days_remaining === 0 ? 'Expired' : `${cert.days_remaining}d remaining`}
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}
