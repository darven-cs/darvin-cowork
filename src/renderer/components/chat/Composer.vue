<template>
  <div class="px-6 pb-5 pt-2">
    <ImportedFilesBar class="mb-1.5" />
    <div class="relative mx-auto max-w-[760px]">
      <!-- spec 39 — `/` 自动补全浮层 -->
      <div
        v-if="showSlashMenu"
        class="absolute bottom-full left-0 right-0 mb-2 overflow-hidden rounded-xl border border-border bg-surface shadow-lg"
      >
        <div class="px-3 py-1.5 text-[11px] font-medium text-text-subtle">
          {{ t('slash.menu.title') }}
        </div>
        <div v-if="matchedSkills.length === 0" class="px-3 py-2 text-sm text-text-subtle">
          {{ t('slash.menu.empty') }}
        </div>
        <div
          v-for="(skill, index) in matchedSkills"
          :key="skill.id"
          :class="[
            'flex cursor-pointer items-center gap-2 px-3 py-2 text-sm',
            index === selectedIndex ? 'bg-primary-muted' : 'hover:bg-surface-hover',
          ]"
          @mousedown.prevent="selectSkill(skill)"
          @mouseenter="selectedIndex = index"
        >
          <div class="min-w-0">
            <div class="font-medium">{{ skill.name }}</div>
            <div class="truncate text-[11px] text-text-subtle">{{ skill.description }}</div>
          </div>
        </div>
      </div>
      <div
        class="rounded-xl border border-border bg-surface-2 transition-colors focus-within:border-border-strong"
      >
        <textarea
          ref="textareaRef"
          v-model="text"
          :placeholder="busy ? t('chat.placeholder.busy') : t('home.prompt.placeholder')"
          :disabled="busy"
          rows="1"
          class="w-full resize-none bg-transparent px-4 pt-3 font-sans text-[14.5px] leading-relaxed text-text outline-none placeholder:text-text-subtle disabled:opacity-50"
          data-testid="composer-textarea"
          @input="onInput"
          @keydown="onKeydown"
        />
        <ComposerToolbar :can-send="canSend" @send="emitSend" @suite="onSuite" @mic="onMic" />
        <ComposerContextRow />
      </div>
    </div>
    <p
      v-if="text.length > 50"
      class="mx-auto mt-1.5 max-w-[760px] text-right font-mono text-[11px] text-text-subtle"
    >
      {{ text.length }}
    </p>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, ref } from 'vue';
import type { DarvinSkillSummary } from '../../../shared/darvin-api';
import { useSkills } from '../../composables/useSkills';
import { t } from '../../services/i18n';
import ComposerToolbar from './ComposerToolbar.vue';
import ComposerContextRow from './ComposerContextRow.vue';
import ImportedFilesBar from './ImportedFilesBar.vue';

const props = defineProps<{ busy: boolean }>();
const emit = defineEmits<{ send: [content: string] }>();

const { skills } = useSkills();
const text = ref<string>('');
const textareaRef = ref<HTMLTextAreaElement | null>(null);
const showSlashMenu = ref(false);
const selectedIndex = ref(0);

const canSend = computed(() => !props.busy && text.value.trim().length > 0);

// spec 39 — 按 `/` 前缀过滤可手动触发的 enabled skill；`//` 转义与多行不弹。
const matchedSkills = computed(() => {
  if (!showSlashMenu.value) return [];
  const filter = text.value.slice(1).split(/\s+/)[0] ?? '';
  return skills.value
    .filter((s) => s.enabled && s.userInvocable)
    .filter((s) => !filter || s.id.toLowerCase().startsWith(filter.toLowerCase()))
    .slice(0, 8);
});

function emitSend() {
  if (!canSend.value) return;
  const content = text.value;
  text.value = '';
  resetHeight();
  showSlashMenu.value = false;
  emit('send', content);
}

function onInput() {
  autoGrow();
  const t = text.value;
  // 空格意味着开始输入 args：菜单收起，否则 `/code-review src/api` 里按 Enter 会重复选中。
  showSlashMenu.value = t.startsWith('/') && !t.startsWith('//') && !t.includes('\n') && !t.includes(' ');
  if (showSlashMenu.value) selectedIndex.value = 0;
}

function onKeydown(e: KeyboardEvent) {
  if (showSlashMenu.value) {
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      if (matchedSkills.value.length > 0) {
        selectedIndex.value = (selectedIndex.value + 1) % matchedSkills.value.length;
      }
      return;
    }
    if (e.key === 'ArrowUp') {
      e.preventDefault();
      if (matchedSkills.value.length > 0) {
        selectedIndex.value = (selectedIndex.value - 1 + matchedSkills.value.length) % matchedSkills.value.length;
      }
      return;
    }
    if (e.key === 'Escape') {
      showSlashMenu.value = false;
      return;
    }
    if (e.key === 'Tab' || (e.key === 'Enter' && !e.shiftKey)) {
      if (matchedSkills.value.length > 0) {
        e.preventDefault();
        selectSkill(matchedSkills.value[selectedIndex.value]);
        return;
      }
    }
  }
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault();
    emitSend();
    return;
  }
  if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
    e.preventDefault();
    emitSend();
  }
}

function selectSkill(skill: DarvinSkillSummary) {
  text.value = `/${skill.id} `;
  showSlashMenu.value = false;
  nextTick(() => {
    textareaRef.value?.focus();
    autoGrow();
  });
}

function autoGrow() {
  const el = textareaRef.value;
  if (!el) return;
  el.style.height = 'auto';
  const max = 200;
  el.style.height = `${Math.min(el.scrollHeight, max)}px`;
}

function resetHeight() {
  const el = textareaRef.value;
  if (el) el.style.height = 'auto';
}

function focus() {
  nextTick(() => textareaRef.value?.focus());
}

defineExpose({ focus });

function onSuite() {}
function onMic() {}
</script>
