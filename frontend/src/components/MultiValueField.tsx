import { ReactNode } from "react";

type MultiValueFieldProps<T> = {
  label: string;
  rows: T[];
  onChange: (rows: T[]) => void;
  renderRow: (row: T, update: (patch: Partial<T>) => void) => ReactNode;
  emptyRow: T;
  addLabel?: string;
};

export function MultiValueField<T extends object>({
  label,
  rows,
  onChange,
  renderRow,
  emptyRow,
  addLabel
}: MultiValueFieldProps<T>) {
  function updateRow(index: number, patch: Partial<T>) {
    onChange(rows.map((row, i) => (i === index ? { ...row, ...patch } : row)));
  }
  function removeRow(index: number) {
    onChange(rows.filter((_, i) => i !== index));
  }
  function addRow() {
    onChange([...rows, emptyRow]);
  }

  return (
    <div className="contacts-multivalue">
      <div className="contacts-multivalue-label">{label}</div>
      {rows.map((row, i) => (
        <div className="contacts-multivalue-row" key={i}>
          {renderRow(row, (patch) => updateRow(i, patch))}
          <button
            type="button"
            className="contacts-multivalue-remove"
            onClick={() => removeRow(i)}
            aria-label={`Remove ${label.toLowerCase()} row`}
          >
            &times;
          </button>
        </div>
      ))}
      <button type="button" className="contacts-multivalue-add" onClick={addRow}>
        {addLabel ?? `+ Add ${label.toLowerCase()}`}
      </button>
    </div>
  );
}
