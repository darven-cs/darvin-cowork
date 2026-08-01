import { describe, it, expect } from 'vitest';
import {
  clampNumber,
  scaleUiText,
  readStoredNumber,
  UI_FONT_DEFAULT,
  UI_FONT_MIN,
  UI_FONT_MAX,
} from './useAppearance';

describe('useAppearance pure helpers', () => {
  it('clampNumber bounds to [min, max]', () => {
    expect(clampNumber(5, 11, 16)).toBe(11);
    expect(clampNumber(20, 11, 16)).toBe(16);
    expect(clampNumber(14, 11, 16)).toBe(14);
  });

  it('scaleUiText scales the base table proportionally to uiFontSize/14', () => {
    // 默认 14 → scale 1，各值等于 @theme 基础字号
    expect(scaleUiText(14)['base']).toBe(14);
    expect(scaleUiText(14)['sm']).toBe(13);
    // 16 → scale 16/14，sm = round(13 * 16/14) = 15
    const big = scaleUiText(16);
    expect(big['base']).toBe(16);
    expect(big['sm']).toBe(15);
    // 11 → sm = round(13 * 11/14) = 10
    expect(scaleUiText(11)['sm']).toBe(10);
  });

  it('readStoredNumber falls back to default when localStorage is unavailable', () => {
    expect(readStoredNumber('darvin.ui-font-size', UI_FONT_DEFAULT, UI_FONT_MIN, UI_FONT_MAX)).toBe(UI_FONT_DEFAULT);
  });
});
