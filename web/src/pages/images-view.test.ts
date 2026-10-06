import { describe, expect, it } from 'vitest';

import type { HostImage } from '../data/types';
import { checkImageArchive, filterImages, IMAGE_UPLOAD_CAP, shortImageId } from './images-view';

const image = (id: string, tags: string[]): HostImage => ({
  id,
  tags,
  size: 1,
  created: '2026-01-01T00:00:00Z',
  inUse: false,
});

describe('shortImageId', () => {
  it('drops the sha256 prefix and keeps 12 characters', () => {
    expect(shortImageId('sha256:0123456789abcdef')).toBe('0123456789ab');
  });

  it('cuts an id without the prefix to 12 characters', () => {
    expect(shortImageId('0123456789abcdef')).toBe('0123456789ab');
  });
});

describe('filterImages', () => {
  const web = image('a', ['web:1']);
  const both = image('b', ['web:1', 'api:2']);
  const untagged = image('c', []);
  const all = [web, both, untagged];

  it('trims and ignores case', () => {
    expect(filterImages(all, ' WEB ')).toEqual([web, both]);
  });

  it('matches when any tag contains the query', () => {
    expect(filterImages(all, 'api')).toEqual([both]);
    expect(filterImages(all, 'eb:')).toEqual([web, both]);
  });

  it('returns the list unchanged for an empty query', () => {
    expect(filterImages(all, '')).toEqual(all);
    expect(filterImages(all, '   ')).toEqual(all);
  });

  it('never matches an untagged image to a non-empty query', () => {
    expect(filterImages([untagged], 'none')).toEqual([]);
  });
});

describe('checkImageArchive', () => {
  it('accepts the archive extensions, case-insensitively', () => {
    expect(checkImageArchive({ name: 'app.tar', size: 1 })).toBeUndefined();
    expect(checkImageArchive({ name: 'app.TAR.GZ', size: 1 })).toBeUndefined();
    expect(checkImageArchive({ name: 'app.tgz', size: 1 })).toBeUndefined();
  });

  it('rejects another extension', () => {
    expect(checkImageArchive({ name: 'app.zip', size: 1 })).toBe(
      'Choose a .tar, .tar.gz, .tgz, .tar.zst, .tar.xz or .tar.bz2 archive',
    );
  });

  it('rejects a file over the cap and accepts one exactly at it', () => {
    expect(checkImageArchive({ name: 'app.tar', size: IMAGE_UPLOAD_CAP + 1 })).toBe(
      'upload exceeds the 10 GiB cap',
    );
    expect(checkImageArchive({ name: 'app.tar', size: IMAGE_UPLOAD_CAP })).toBeUndefined();
  });
});
