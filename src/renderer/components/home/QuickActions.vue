<template>
  <!--
    4 个 quick-action tile：颜色 / icon 来自已有 qa-* token
    hover glow 由 theme.css 的 .qa-tile--* hover 规则驱动
  -->
  <div class="grid w-full grid-cols-2 gap-3 sm:grid-cols-4">
    <button
      v-for="tile in tiles"
      :key="tile.id"
      type="button"
      class="qa-tile flex items-center gap-3 rounded-xl border border-border bg-surface-2 px-3.5 py-3 text-left transition-colors hover:border-border-strong hover:bg-surface-hover"
      :class="`qa-tile--${tile.id}`"
      @click="onClick(tile.id)"
    >
      <span class="icon-wrap flex h-8 w-8 shrink-0 items-center justify-center rounded-lg">
        <Icon :name="tile.icon" :size="16" />
      </span>
      <span class="flex min-w-0 flex-col">
        <span class="truncate font-sans text-[13px] font-medium text-text">{{ t(tile.titleKey) }}</span>
        <span class="truncate font-sans text-[11px] text-text-muted">{{ t(tile.descKey) }}</span>
      </span>
    </button>
  </div>
</template>

<script setup lang="ts">
import { t } from '../../services/i18n';
import Icon from '../common/Icon.vue';

type TileId = 'qa-slide' | 'qa-data' | 'qa-doc' | 'qa-web';

interface Tile {
  id: TileId;
  icon: string;
  titleKey: string;
  descKey: string;
}

const tiles: Tile[] = [
  { id: 'qa-slide', icon: 'qa-slide', titleKey: 'quick.slide.title', descKey: 'quick.slide.desc' },
  { id: 'qa-data',  icon: 'qa-data',  titleKey: 'quick.data.title',  descKey: 'quick.data.desc'  },
  { id: 'qa-doc',   icon: 'qa-doc',   titleKey: 'quick.doc.title',   descKey: 'quick.doc.desc'   },
  { id: 'qa-web',   icon: 'qa-web',   titleKey: 'quick.web.title',   descKey: 'quick.web.desc'   },
];

const emit = defineEmits<{
  select: [id: TileId];
}>();

function onClick(id: TileId) {
  emit('select', id);
}
</script>