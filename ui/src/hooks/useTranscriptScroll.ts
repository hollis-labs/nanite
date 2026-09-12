import { useCallback, useLayoutEffect, useRef, useState } from "react";
import type { TranscriptPosition } from "@/lib/transcript-position";
import { useChatStore } from "@/stores/useChatStore";

export function useTranscriptScroll(
  sessionId: string | null,
  ready: boolean,
  oldestOffset: number,
  contentVersion: string,
  jumping: boolean,
  followRequest: string | null,
) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const bottomRef = useRef<HTMLDivElement>(null);
  const position = useRef<TranscriptPosition | null>(null);
  const initialized = useRef(false);
  const following = useRef(true);
  const programmatic = useRef(false);
  const [userHasScrolled, setUserHasScrolled] = useState(false);

  const capture = useCallback(() => {
    const el = scrollRef.current;
    if (!sessionId || !el || !initialized.current || !el.clientHeight) return;
    const top = el.getBoundingClientRect().top;
    const anchor = Array.from(el.querySelectorAll<HTMLElement>("[data-message-id]")).find(
      (node) => node.getBoundingClientRect().bottom > top,
    );
    position.current = {
      anchorId: anchor?.dataset.messageId ?? null,
      offset: anchor ? anchor.getBoundingClientRect().top - top : 0,
      atBottom: following.current,
      oldestOffset,
    };
    useChatStore.getState().saveTranscriptPosition(sessionId, position.current);
  }, [sessionId, oldestOffset]);

  const detach = useCallback(() => {
    following.current = false;
    programmatic.current = false;
    setUserHasScrolled(true);
  }, []);

  const scrollToBottom = useCallback(() => {
    const el = scrollRef.current;
    if (!el) return;
    following.current = true;
    programmatic.current = true;
    setUserHasScrolled(false);
    el.scrollTo({ top: el.scrollHeight, behavior: "smooth" });
  }, []);

  useLayoutEffect(() => {
    const el = scrollRef.current;
    if (!el || !ready || !sessionId) return;
    position.current = useChatStore.getState().getTranscriptPosition(sessionId);
    following.current = position.current?.atBottom ?? true;
    initialized.current = false;

    const restore = (behavior: ScrollBehavior = "instant") => {
      if (!el.clientHeight || jumping) return;
      const saved = position.current;
      programmatic.current = true;
      if (following.current) {
        el.scrollTo({ top: el.scrollHeight, behavior });
      } else if (saved?.anchorId) {
        const anchor = Array.from(el.querySelectorAll<HTMLElement>("[data-message-id]")).find(
          (node) => node.dataset.messageId === saved.anchorId,
        );
        if (anchor)
          el.scrollTop +=
            anchor.getBoundingClientRect().top - el.getBoundingClientRect().top - saved.offset;
      }
      initialized.current = true;
      setUserHasScrolled(!following.current);
    };
    restore();

    const userInput = () => {
      programmatic.current = false;
    };
    const onScroll = () => {
      if (!initialized.current || !el.clientHeight) return;
      if (!programmatic.current) {
        const distance = el.scrollHeight - el.scrollTop - el.clientHeight;
        if (distance > 100) following.current = false;
        else if (distance < 50) following.current = true;
        setUserHasScrolled(!following.current);
      }
      capture();
    };
    el.addEventListener("scroll", onScroll);
    el.addEventListener("wheel", userInput, { passive: true });
    el.addEventListener("touchstart", userInput, { passive: true });
    el.addEventListener("pointerdown", userInput);
    el.addEventListener("keydown", userInput);
    // Images, envelopes and markdown can change height after first paint.
    // Restore relative to a message, not a pixel offset in the whole document.
    const observer = new ResizeObserver(() => restore(initialized.current ? "smooth" : "instant"));
    if (el.firstElementChild) observer.observe(el.firstElementChild);
    return () => {
      capture();
      observer.disconnect();
      el.removeEventListener("scroll", onScroll);
      el.removeEventListener("wheel", userInput);
      el.removeEventListener("touchstart", userInput);
      el.removeEventListener("pointerdown", userInput);
      el.removeEventListener("keydown", userInput);
    };
  }, [sessionId, ready, capture, jumping]);

  // biome-ignore lint/correctness/useExhaustiveDependencies: content changes trigger DOM-based scroll following.
  useLayoutEffect(() => {
    const el = scrollRef.current;
    if (!el || !initialized.current || !following.current || jumping || !el.clientHeight) return;
    programmatic.current = true;
    el.scrollTo({ top: el.scrollHeight, behavior: "smooth" });
  }, [contentVersion, jumping]);

  useLayoutEffect(() => {
    if (followRequest) scrollToBottom();
  }, [followRequest, scrollToBottom]);

  return { scrollRef, bottomRef, userHasScrolled, scrollToBottom, detach };
}
