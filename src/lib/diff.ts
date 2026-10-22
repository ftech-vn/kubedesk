/** DiffLine is a line of a unified diff. */
export interface DiffLine {
  kind: "same" | "add" | "del";
  text: string;
  /** a and b are the line numbers in the old and the new text. */
  a?: number;
  b?: number;
}

/**
 * diffLines compares two texts line by line, with the longest common
 * subsequence. Texts too long for that come back as a removal and an
 * addition.
 */
export function diffLines(before: string, after: string): DiffLine[] {
  const a = before ? before.replace(/\n$/, "").split("\n") : [];
  const b = after ? after.replace(/\n$/, "").split("\n") : [];
  // Lines alike at both ends need no table.
  let start = 0;
  while (start < a.length && start < b.length && a[start] === b[start]) start++;
  let endA = a.length;
  let endB = b.length;
  while (endA > start && endB > start && a[endA - 1] === b[endB - 1]) {
    endA--;
    endB--;
  }
  const out: DiffLine[] = [];
  for (let i = 0; i < start; i++) out.push({ kind: "same", text: a[i]!, a: i + 1, b: i + 1 });
  const ma = endA - start;
  const mb = endB - start;
  if (ma * mb > 4_000_000) {
    for (let i = start; i < endA; i++) out.push({ kind: "del", text: a[i]!, a: i + 1 });
    for (let j = start; j < endB; j++) out.push({ kind: "add", text: b[j]!, b: j + 1 });
  } else {
    // lcs[i][j] is the common length of a[start+i..] and b[start+j..].
    const lcs: Uint32Array[] = Array.from({ length: ma + 1 }, () => new Uint32Array(mb + 1));
    for (let i = ma - 1; i >= 0; i--) {
      for (let j = mb - 1; j >= 0; j--) {
        lcs[i]![j] = a[start + i] === b[start + j] ? lcs[i + 1]![j + 1]! + 1 : Math.max(lcs[i + 1]![j]!, lcs[i]![j + 1]!);
      }
    }
    let i = 0;
    let j = 0;
    while (i < ma || j < mb) {
      if (i < ma && j < mb && a[start + i] === b[start + j]) {
        out.push({ kind: "same", text: a[start + i]!, a: start + i + 1, b: start + j + 1 });
        i++;
        j++;
      } else if (j < mb && (i === ma || lcs[i]![j + 1]! >= lcs[i + 1]![j]!)) {
        out.push({ kind: "add", text: b[start + j]!, b: start + j + 1 });
        j++;
      } else {
        out.push({ kind: "del", text: a[start + i]!, a: start + i + 1 });
        i++;
      }
    }
  }
  for (let k = 0; k < a.length - endA; k++) out.push({ kind: "same", text: a[endA + k]!, a: endA + k + 1, b: endB + k + 1 });
  return out;
}

/** Hunk is a run of shown lines, or a count of hidden unchanged ones. */
export type Hunk = { lines: DiffLine[] } | { skipped: number };

/** hunks keeps `context` unchanged lines around each change. */
export function hunks(lines: DiffLine[], context = 3): Hunk[] {
  const keep = new Array(lines.length).fill(false);
  lines.forEach((l, i) => {
    if (l.kind === "same") return;
    for (let k = Math.max(0, i - context); k <= Math.min(lines.length - 1, i + context); k++) keep[k] = true;
  });
  const out: Hunk[] = [];
  let run: DiffLine[] = [];
  let skipped = 0;
  lines.forEach((l, i) => {
    if (keep[i]) {
      if (skipped) out.push({ skipped });
      skipped = 0;
      run.push(l);
    } else {
      if (run.length) out.push({ lines: run });
      run = [];
      skipped++;
    }
  });
  if (run.length) out.push({ lines: run });
  if (skipped) out.push({ skipped });
  return out;
}
