import type {CSSProperties, ReactNode} from 'react';
import {useEffect, useId, useRef, useState} from 'react';
import {createPortal} from 'react-dom';

import {glossaryById} from '@site/src/generated/glossary';
import styles from './styles.module.css';

type Position = CSSProperties & {top: number; left: number; width: number};

export default function Term({id, children}: {id: string; children: ReactNode}): React.JSX.Element {
  const term = glossaryById[id];
  if (!term) {
    throw new Error(`Unknown glossary term ${id}`);
  }

  const triggerRef = useRef<HTMLButtonElement>(null);
  const tooltipRef = useRef<HTMLSpanElement>(null);
  const wasFocusedOnPointerDownRef = useRef(false);
  const tooltipId = `term-${useId().replace(/:/g, '')}`;
  const [mounted, setMounted] = useState(false);
  const [open, setOpen] = useState(false);
  const [position, setPosition] = useState<Position | null>(null);

  useEffect(() => setMounted(true), []);

  useEffect(() => {
    if (!open) {
      setPosition(null);
      return undefined;
    }

    const place = () => {
      const trigger = triggerRef.current;
      const tooltip = tooltipRef.current;
      if (!trigger || !tooltip) {
        return;
      }
      const margin = 16;
      const gap = 10;
      const width = Math.min(320, window.innerWidth - margin * 2);
      const triggerBox = trigger.getBoundingClientRect();
      const tooltipBox = tooltip.getBoundingClientRect();
      const centered = triggerBox.left + triggerBox.width / 2 - width / 2;
      const left = Math.max(margin, Math.min(centered, window.innerWidth - width - margin));
      const below = triggerBox.bottom + gap;
      const top = below + tooltipBox.height <= window.innerHeight - margin
        ? below
        : Math.max(margin, triggerBox.top - tooltipBox.height - gap);
      setPosition({left, top, width});
    };
    const frame = window.requestAnimationFrame(place);
    window.addEventListener('resize', place);
    window.addEventListener('scroll', place, true);
    return () => {
      window.cancelAnimationFrame(frame);
      window.removeEventListener('resize', place);
      window.removeEventListener('scroll', place, true);
    };
  }, [open]);

  useEffect(() => {
    if (!open) {
      return undefined;
    }
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setOpen(false);
        triggerRef.current?.focus();
      }
    };
    document.addEventListener('keydown', closeOnEscape);
    return () => document.removeEventListener('keydown', closeOnEscape);
  }, [open]);

  const tooltip = open && mounted
    ? createPortal(
        <span
          className={styles.tooltip}
          id={tooltipId}
          ref={tooltipRef}
          role="tooltip"
          style={position ?? {left: 0, top: 0, visibility: 'hidden', width: 320}}>
          <span className={styles.tooltipTerm}>{term.term}</span>
          <span>{term.definition}</span>
        </span>,
        document.body,
      )
    : null;

  return (
    <span
      className={styles.wrapper}
      onPointerEnter={(event) => {
        if (event.pointerType === 'mouse') setOpen(true);
      }}
      onPointerLeave={(event) => {
        if (event.pointerType === 'mouse' && document.activeElement !== triggerRef.current) {
          setOpen(false);
        }
      }}>
      <button
        aria-describedby={open ? tooltipId : undefined}
        className={styles.trigger}
        onBlur={() => setOpen(false)}
        onClick={(event) => {
          if (event.detail === 0 || wasFocusedOnPointerDownRef.current) {
            setOpen((current) => !current);
          } else {
            setOpen(true);
          }
          wasFocusedOnPointerDownRef.current = false;
        }}
        onFocus={() => setOpen(true)}
        onPointerCancel={() => {
          wasFocusedOnPointerDownRef.current = false;
        }}
        onPointerDown={() => {
          wasFocusedOnPointerDownRef.current = document.activeElement === triggerRef.current;
        }}
        ref={triggerRef}
        type="button">
        <dfn>{children}</dfn>
      </button>
      {tooltip}
    </span>
  );
}
