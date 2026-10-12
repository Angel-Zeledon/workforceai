/** RFC 4180-ish parser: quotes, escaped quotes, CRLF; the delimiter is guessed from the header line. Bounded. */
export function parseCsv(text: string, maxRows = 2000): string[][] {
  const first = text.split(/\r?\n/, 1)[0] ?? "";
  const counts: [string, number][] = [",", "\t", ";"].map((d) => [d, first.split(d).length]);
  const delim = counts.sort((a, b) => b[1] - a[1])[0][0];
  const rows: string[][] = [];
  let row: string[] = [], cell = "", q = false;
  for (let i = 0; i < text.length && rows.length < maxRows; i++) {
    const c = text[i];
    if (q) {
      if (c === '"') { if (text[i + 1] === '"') { cell += '"'; i++; } else q = false; } else cell += c;
    } else if (c === '"' && cell === "") q = true;
    else if (c === delim) { row.push(cell); cell = ""; }
    else if (c === "\n" || c === "\r") {
      if (c === "\r" && text[i + 1] === "\n") i++;
      row.push(cell); cell = "";
      if (row.length > 1 || row[0] !== "") rows.push(row);
      row = [];
    } else cell += c;
  }
  if (rows.length < maxRows && (cell !== "" || row.length)) { row.push(cell); rows.push(row); }
  return rows;
}
