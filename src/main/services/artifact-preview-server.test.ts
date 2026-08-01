import { describe, expect, it } from 'vitest';
import path from 'node:path';
import { resolveWithinRoot } from './artifact-preview-server';

const root = path.resolve('/tmp/darvin-ws');
const sub = path.join(root, 'sub');

describe('resolveWithinRoot (artifact preview server)', () => {
  it('resolves a sibling asset inside the workspace root', () => {
    const target = resolveWithinRoot(root, sub, 'style.css');
    expect(target).toBe(path.join(sub, 'style.css'));
  });

  it('resolves an asset in a nested directory inside the root', () => {
    const target = resolveWithinRoot(root, sub, 'assets/app.js');
    expect(target).toBe(path.join(sub, 'assets', 'app.js'));
  });

  it('returns null when the requested path escapes the root', () => {
    const target = resolveWithinRoot(root, sub, '../../secret');
    expect(target).toBeNull();
  });

  it('serves the entry file itself for an empty rel path', () => {
    const target = resolveWithinRoot(root, sub, '');
    expect(target).toBe(sub);
  });
});
