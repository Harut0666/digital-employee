// 对话区贴底：新消息、逐字增长的回复、卡片展开、窗口缩放，只要人没往上翻就一直停在底部。
// 之前只在“最后一条的 key / 条数”变化时平滑滚一次：流式回复的 key 不变、切会话时内容还在加载，都会停在半截。

import { useCallback, useEffect, useRef, type RefObject } from "react";
import { isNearBottom } from "../view/scroll-follow";

/**
 * 让 ref 指向的滚动容器贴底。resetKey 变了（切会话）就重新贴底。
 * 返回 pin：发消息时调用，无论之前翻到哪都回到底部。
 */
export function useStickToBottom(ref: RefObject<HTMLElement | null>, resetKey: string | undefined): () => void {
  const pinned = useRef(true);

  const toBottom = useCallback(() => {
    const el = ref.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [ref]);

  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    pinned.current = true;
    toBottom();

    const stick = () => {
      if (pinned.current) toBottom();
    };
    // 人往上翻就不再强制贴底；翻回底部附近恢复。
    // 只认人的操作引起的滚动：程序自己贴底后，scroll 事件到达时内容可能又变高了（换行、卡片展开），
    // 那时再量“离底部多远”会误判成人往上翻，从此不再贴底。
    let userScrolling = false;
    let userTimer: ReturnType<typeof setTimeout> | undefined;
    const markUser = () => {
      userScrolling = true;
      clearTimeout(userTimer);
      userTimer = setTimeout(() => {
        userScrolling = false;
      }, 300);
    };
    const dragStart = () => {
      userScrolling = true; // 拖滚动条：按住期间都算
      clearTimeout(userTimer);
    };
    const dragEnd = () => markUser();
    const onKey = (event: KeyboardEvent) => {
      if (["PageUp", "PageDown", "Home", "End", "ArrowUp", "ArrowDown", " "].includes(event.key)) markUser();
    };
    const onScroll = () => {
      if (userScrolling) pinned.current = isNearBottom(el);
    };
    el.addEventListener("scroll", onScroll, { passive: true });
    el.addEventListener("wheel", markUser, { passive: true });
    el.addEventListener("touchmove", markUser, { passive: true });
    el.addEventListener("pointerdown", dragStart);
    window.addEventListener("pointerup", dragEnd);
    window.addEventListener("keydown", onKey);

    const resize = new ResizeObserver(stick);
    resize.observe(el); // 容器本身变高变矮（顶部的步骤、轨迹展开收起、窗口缩放）
    const watched = new Set<Element>();
    const watchChildren = () => {
      for (const child of Array.from(el.children)) {
        if (watched.has(child)) continue;
        watched.add(child);
        resize.observe(child); // 单条内容变高（卡片展开、图片加载完）
      }
    };
    watchChildren();
    const mutation = new MutationObserver(() => {
      watchChildren();
      stick(); // 新增条目、流式文字增长
    });
    mutation.observe(el, { childList: true, subtree: true, characterData: true });

    return () => {
      clearTimeout(userTimer);
      el.removeEventListener("scroll", onScroll);
      el.removeEventListener("wheel", markUser);
      el.removeEventListener("touchmove", markUser);
      el.removeEventListener("pointerdown", dragStart);
      window.removeEventListener("pointerup", dragEnd);
      window.removeEventListener("keydown", onKey);
      resize.disconnect();
      mutation.disconnect();
    };
  }, [ref, resetKey, toBottom]);

  return useCallback(() => {
    pinned.current = true;
    toBottom();
  }, [toBottom]);
}
