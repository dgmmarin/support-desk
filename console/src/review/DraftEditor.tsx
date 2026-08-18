export function DraftEditor({ value, onChange, unsupported }: { value: string; onChange: (v: string) => void; unsupported: string[] }) {
  return (
    <div style={{ display: "grid", gap: 4 }}>
      {unsupported.length > 0 && <p role="status" style={{ color: "var(--warn)" }}>⚠ {unsupported.length} unsupported claim(s): {unsupported.join("; ")}</p>}
      <textarea aria-label="draft reply" value={value} onChange={(e) => onChange(e.target.value)} rows={16} style={{ width: "100%", fontFamily: "inherit" }} />
    </div>
  );
}
