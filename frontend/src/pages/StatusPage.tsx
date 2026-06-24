import { useEffect, useState } from "react";
import { getJSON } from "../api/client";

type Status = {
  scanIntervalSeconds: number;
  checkpoint: string;
};

export function StatusPage() {
  const [status, setStatus] = useState<Status | null>(null);

  useEffect(() => {
    getJSON<Status>("/api/status").then(setStatus).catch(() => setStatus(null));
  }, []);

  return (
    <section className="panel">
      <h2>Run Status</h2>
      {status ? (
        <pre>{JSON.stringify(status, null, 2)}</pre>
      ) : (
        <p>Waiting for backend status endpoint.</p>
      )}
    </section>
  );
}
