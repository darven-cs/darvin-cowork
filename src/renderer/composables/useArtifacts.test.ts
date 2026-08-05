import { beforeEach, describe, expect, it } from 'vitest';
import { useArtifacts, ArtifactContentView, ArtifactSpecialTab, previewTabId } from './useArtifacts';
import { useSession } from './useSession';

const artifacts = useArtifacts();
const session = useSession();

beforeEach(() => {
  artifacts.reset();
  session.activeSessionId.value = 's1';
});

describe('useArtifacts addArtifact', () => {
  it('creates the artifact and opens an active preview tab', () => {
    artifacts.addArtifact('s1', { id: 'a1', kind: 'html', name: 'page', content: '<html/>', createdAt: 1 });
    expect(artifacts.artifactsBySession.value.s1).toHaveLength(1);
    expect(artifacts.artifactsBySession.value.s1[0].name).toBe('page');
    expect(artifacts.previewTabsBySession.value.s1).toHaveLength(1);
    expect(artifacts.activeTabIdBySession.value.s1).toBe(previewTabId('a1'));
    expect(artifacts.isPanelOpenBySession.value.s1).toBe(true);
  });

  it('dedupes the same artifact id without duplicating the tab', () => {
    artifacts.addArtifact('s1', { id: 'a1', kind: 'html', content: 'v1', createdAt: 1 });
    artifacts.addArtifact('s1', { id: 'a1', kind: 'html', content: 'v1', createdAt: 1 });
    expect(artifacts.artifactsBySession.value.s1).toHaveLength(1);
    expect(artifacts.previewTabsBySession.value.s1).toHaveLength(1);
  });

  it('updates content when the same artifact id re-emits new content', () => {
    artifacts.addArtifact('s1', { id: 'a1', kind: 'html', content: 'v1', createdAt: 1 });
    artifacts.addArtifact('s1', { id: 'a1', kind: 'html', content: 'v2', createdAt: 2 });
    expect(artifacts.artifactsBySession.value.s1).toHaveLength(1);
    expect(artifacts.artifactsBySession.value.s1[0].content).toBe('v2');
  });

  it('addArtifact with openPanel:false registers a tab without opening the panel', () => {
    artifacts.addArtifact('s1', { id: 'a1', kind: 'text', content: 'x', createdAt: 1 }, { openPanel: false });
    expect(artifacts.artifactsBySession.value.s1).toHaveLength(1);
    expect(artifacts.previewTabsBySession.value.s1).toHaveLength(1);
    expect(artifacts.activeTabIdBySession.value.s1).toBe(previewTabId('a1'));
    expect(artifacts.isPanelOpenBySession.value.s1).toBeUndefined();
  });

  it('isolates artifacts by session', () => {
    artifacts.addArtifact('s1', { id: 'a1', kind: 'text', content: 'x', createdAt: 1 });
    artifacts.addArtifact('s2', { id: 'a2', kind: 'text', content: 'y', createdAt: 2 });
    expect(artifacts.artifactsBySession.value.s1).toHaveLength(1);
    expect(artifacts.artifactsBySession.value.s2).toHaveLength(1);
    session.activeSessionId.value = 's2';
    expect(artifacts.currentArtifacts.value[0].id).toBe('a2');
  });
});

describe('useArtifacts tabs', () => {
  it('closePreviewTab falls back to the next tab, then fileList', () => {
    artifacts.addArtifact('s1', { id: 'a1', kind: 'text', content: 'x', createdAt: 1 });
    artifacts.addArtifact('s1', { id: 'a2', kind: 'text', content: 'y', createdAt: 2 });
    expect(artifacts.activeTabIdBySession.value.s1).toBe(previewTabId('a2'));

    artifacts.closePreviewTab('s1', previewTabId('a2'));
    expect(artifacts.activeTabIdBySession.value.s1).toBe(previewTabId('a1'));

    artifacts.closePreviewTab('s1', previewTabId('a1'));
    expect(artifacts.activeTabIdBySession.value.s1).toBe(ArtifactSpecialTab.FileList);
  });

  it('setContentView toggles the tab content view', () => {
    artifacts.addArtifact('s1', { id: 'a1', kind: 'code', content: 'x', createdAt: 1 });
    artifacts.setContentView('s1', previewTabId('a1'), ArtifactContentView.Code);
    expect(artifacts.previewTabsBySession.value.s1[0].contentView).toBe(ArtifactContentView.Code);
  });

  it('activeArtifact returns the active artifact and null for special tabs', () => {
    artifacts.addArtifact('s1', { id: 'a1', kind: 'code', content: 'x', createdAt: 1 });
    expect(artifacts.activeArtifact()?.id).toBe('a1');
    artifacts.activateTab('s1', ArtifactSpecialTab.Browser);
    expect(artifacts.activeArtifact()).toBeNull();
  });
});

describe('useArtifacts panel width', () => {
  it('clamps to 180-1000', () => {
    artifacts.setPanelWidth(50);
    expect(artifacts.panelWidth.value).toBe(180);
    artifacts.setPanelWidth(2000);
    expect(artifacts.panelWidth.value).toBe(1000);
    artifacts.setPanelWidth(640);
    expect(artifacts.panelWidth.value).toBe(640);
  });
});

describe('useArtifacts cleanup', () => {
  it('clearSessionArtifacts removes all per-session state', () => {
    artifacts.addArtifact('s1', { id: 'a1', kind: 'text', content: 'x', createdAt: 1 });
    artifacts.clearSessionArtifacts('s1');
    expect(artifacts.artifactsBySession.value.s1).toBeUndefined();
    expect(artifacts.previewTabsBySession.value.s1).toBeUndefined();
    expect(artifacts.activeTabIdBySession.value.s1).toBeUndefined();
    expect(artifacts.isPanelOpenBySession.value.s1).toBeUndefined();
  });
});
