// The folders picked in Settings for new notes and the daily note. Each is
// stored as a full path, its space first, so the folder also names the
// space. A folder that is not there yet is made by the first note in it.

/** The folder when its space is one of these; empty when unset or its
 * space is gone (or not shared with this account any more). */
export function pinned(folder: string, spaces: string[]): string {
  return folder !== '' && spaces.includes(folder.split('/')[0] ?? '') ? folder : ''
}

/** Today's note's path inside its space. With a folder picked, the
 * pattern's leading folders without a date token give way to it (empty:
 * the top of the space); without one the pattern stands. Mirrors inFolder
 * in internal/server/capture.go. date is YYYY-MM-DD. */
export function dailyPath(pattern: string, folder: string | undefined, date: string): string {
  let rel = pattern
  if (folder !== undefined) {
    const parts = pattern.split('/')
    let i = 0
    while (i < parts.length - 1 && !parts[i]!.includes('{')) i++
    rel = parts.slice(i).join('/')
    if (folder) rel = folder + '/' + rel
  }
  const [y = '', m = '', d = ''] = date.split('-')
  return rel
    .replaceAll('{YYYY}', y)
    .replaceAll('{MM}', m)
    .replaceAll('{DD}', d)
    .replaceAll('{date}', date)
}
