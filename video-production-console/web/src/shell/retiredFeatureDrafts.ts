export function clearRetiredFeatureDrafts(storage: Pick<Storage, "length" | "key" | "removeItem">): void {
  for (let index = storage.length - 1; index >= 0; index--) {
    const key = storage.key(index);
    if (key?.startsWith("console:ai-short-draft:")) storage.removeItem(key);
  }
}
