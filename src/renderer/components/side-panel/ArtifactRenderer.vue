<template>
  <component :is="rendererFor(artifact.kind)" :artifact="artifact" />
</template>

<script setup lang="ts">
import type { Component } from 'vue';
import type { Artifact } from '../../composables/useArtifacts';
import HtmlRenderer from './renderers/HtmlRenderer.vue';
import SvgRenderer from './renderers/SvgRenderer.vue';
import ImageRenderer from './renderers/ImageRenderer.vue';
import VideoRenderer from './renderers/VideoRenderer.vue';
import MermaidRenderer from './renderers/MermaidRenderer.vue';
import CodeRenderer from './renderers/CodeRenderer.vue';
import MarkdownRenderer from './renderers/MarkdownRenderer.vue';
import TextRenderer from './renderers/TextRenderer.vue';
import DocumentRenderer from './renderers/DocumentRenderer.vue';
import LocalServiceRenderer from './renderers/LocalServiceRenderer.vue';

defineProps<{ artifact: Artifact }>();

const rendererFor = (kind: Artifact['kind']): Component => {
  switch (kind) {
    case 'html':           return HtmlRenderer;
    case 'svg':            return SvgRenderer;
    case 'image':          return ImageRenderer;
    case 'video':          return VideoRenderer;
    case 'mermaid':        return MermaidRenderer;
    case 'code':           return CodeRenderer;
    case 'markdown':       return MarkdownRenderer;
    case 'text':           return TextRenderer;
    case 'document':       return DocumentRenderer;
    case 'local-service':  return LocalServiceRenderer;
    default:               return TextRenderer;
  }
};
</script>
