import type { ReactNode } from 'react';
import { Tooltip } from 'antd';
import { InfoCircleOutlined } from '@ant-design/icons';

/** Unified window toolbar: drags the window, keeps controls clickable. */
export function Toolbar({ title, subtitle, leading, center, actions }: {
  title?: ReactNode; subtitle?: ReactNode; leading?: ReactNode; center?: ReactNode; actions?: ReactNode;
}) {
  return <header className="toolbar drag-region">
    <div className="toolbar-title">
      {leading}
      <div className="toolbar-heading">
        {title && <h1>{title}</h1>}
        {subtitle && <div className="toolbar-subtitle">{subtitle}</div>}
      </div>
    </div>
    {center && <div className="toolbar-center">{center}</div>}
    <div className="toolbar-actions">{actions}</div>
  </header>;
}

/** Scrollable content area under the toolbar. */
export function Page({ children, wide, className }: { children: ReactNode; wide?: boolean; className?: string }) {
  return <div className={`page-scroll${className ? ' ' + className : ''}`}>
    <div className={wide ? 'page page-wide' : 'page'}>{children}</div>
  </div>;
}

/** System Settings style grouped inset section. */
export function Group({ title, extra, footer, children, className, id, plain, label }: {
  title?: ReactNode; extra?: ReactNode; footer?: ReactNode; children?: ReactNode;
  className?: string; id?: string; plain?: boolean; label?: string;
}) {
  return <section className={`group${className ? ' ' + className : ''}`} id={id} aria-label={label}>
    {(title || extra) && <div className="group-header">
      {title && <h2>{title}</h2>}
      {extra && <div className="group-extra">{extra}</div>}
    </div>}
    {children !== undefined && <div className={plain ? 'group-plain' : 'group-card'}>{children}</div>}
    {footer && <div className="group-footer">{footer}</div>}
  </section>;
}

/** One row inside a group card: label on the left, value or control on the right. */
export function Row({ label, children, detail, className }: { label: ReactNode; children?: ReactNode; detail?: ReactNode; className?: string }) {
  return <div className={`row${className ? ' ' + className : ''}`}>
    <div className="row-label">
      <div>{label}</div>
      {detail && <small>{detail}</small>}
    </div>
    <div className="row-value">{children}</div>
  </div>;
}

export type Tone = 'green' | 'orange' | 'red' | 'blue' | 'gray';

/** A status shown as a small coloured dot and plain text. */
export function Status({ tone = 'gray', children, title }: { tone?: Tone; children: ReactNode; title?: string }) {
  return <span className={`status status-${tone}`} title={title}><i aria-hidden="true" />{children}</span>;
}

/** Secondary explanations live behind an info icon instead of banners. */
export function InfoTip({ title, children }: { title: ReactNode; children?: ReactNode }) {
  return <Tooltip title={<div className="info-tip"><strong>{title}</strong>{children && <div>{children}</div>}</div>}>
    <button type="button" className="info-button" aria-label={typeof title === 'string' ? title : '说明'}>
      <InfoCircleOutlined aria-hidden="true" />
    </button>
  </Tooltip>;
}

/** Inline notice for things the person must not miss (warnings, limits). */
export function Notice({ tone = 'gray', title, children, action }: {
  tone?: Tone; title: ReactNode; children?: ReactNode; action?: ReactNode;
}) {
  return <div className={`notice notice-${tone}`} role={tone === 'red' || tone === 'orange' ? 'alert' : undefined}>
    <i aria-hidden="true" />
    <div className="notice-body"><strong>{title}</strong>{children && <div>{children}</div>}</div>
    {action && <div className="notice-action">{action}</div>}
  </div>;
}

export function EmptyState({ icon, title, children, action }: { icon?: ReactNode; title: ReactNode; children?: ReactNode; action?: ReactNode }) {
  return <div className="empty-state">
    {icon && <div className="empty-icon" aria-hidden="true">{icon}</div>}
    <div className="empty-title">{title}</div>
    {children && <div className="empty-text">{children}</div>}
    {action && <div className="empty-action">{action}</div>}
  </div>;
}

export const formatTime = (value?: string) => value ? new Date(value).toLocaleString() : '记录未提供';
