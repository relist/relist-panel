import type { HostImage } from '../data/types';

/**
 * The Images page's pure rules: how an image id is shortened, how the search
 * box filters the list, and the pre-flight check on an archive before it is
 * uploaded. Kept pure so they are testable without a DOM.
 */

/** The server's upload cap (10 GiB); the copy below matches the server's message. */
export const IMAGE_UPLOAD_CAP = 10 * 2 ** 30;

export const IMAGE_ARCHIVE_EXTENSIONS = [
  '.tar',
  '.tar.gz',
  '.tgz',
  '.tar.zst',
  '.tar.xz',
  '.tar.bz2',
] as const;

/** `sha256:0123456789abcdef…` → the first 12 characters of the digest. */
export function shortImageId(id: string): string {
  return id.replace(/^sha256:/, '').slice(0, 12);
}

/** Case-insensitive, trimmed substring match on tags; an untagged image matches no non-empty query. */
export function filterImages(images: HostImage[], query: string): HostImage[] {
  const needle = query.trim().toLowerCase();
  if (needle === '') return images;
  return images.filter((image) => image.tags.some((tag) => tag.toLowerCase().includes(needle)));
}

/** The error copy for a file that cannot be uploaded, or `undefined` when it is acceptable. */
export function checkImageArchive(file: { name: string; size: number }): string | undefined {
  const name = file.name.toLowerCase();
  if (!IMAGE_ARCHIVE_EXTENSIONS.some((extension) => name.endsWith(extension))) {
    return 'Choose a .tar, .tar.gz, .tgz, .tar.zst, .tar.xz or .tar.bz2 archive';
  }
  if (file.size > IMAGE_UPLOAD_CAP) return 'upload exceeds the 10 GiB cap';
  return undefined;
}
