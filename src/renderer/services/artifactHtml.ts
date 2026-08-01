/**
 * artifact HTML 沙箱内锚点跳转：sandbox="allow-scripts" 的 iframe 里
 * <a href="#id"> 默认失效，注入拦截器恢复页内平滑滚动。
 */

// 拆开拼 <script> 闭合标签，避免 .ts / .vue 源码里出现字面 </script> 序列
const SCRIPT_END = '</scr' + 'ipt>';

const HASH_NAV_INTERCEPTOR =
  `<script>document.addEventListener('click',function(e){var a=e.target&&(e.target.closest?a.target.closest('a'):e.target);if(!a||a.tagName!=='A')return;var h=a.getAttribute('href');if(!h||h.charAt(0)!=='#')return;e.preventDefault();var id=h.slice(1);if(!id){window.scrollTo({top:0,behavior:'smooth'});return;}var el=document.getElementById(id)||document.querySelector('[name="'+id+'"]');if(el)el.scrollIntoView({behavior:'smooth'});});` +
  SCRIPT_END;

export function injectHashNavInterceptor(html: string): string {
  if (html.includes('</body>')) return html.replace('</body>', `${HASH_NAV_INTERCEPTOR}</body>`);
  if (html.includes('</html>')) return html.replace('</html>', `${HASH_NAV_INTERCEPTOR}</html>`);
  return html + HASH_NAV_INTERCEPTOR;
}
