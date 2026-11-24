// ubah nilai sel dari API jadi teks. NULL dibedain dari string "NULL",
// object/array (json, jsonb, array postgres) ditampilin sebagai JSON
export function formatCell(value) {
  if (value === null || value === undefined) return { text: 'NULL', isNull: true }
  if (typeof value === 'object') return { text: JSON.stringify(value), isNull: false }
  return { text: String(value), isNull: false }
}
