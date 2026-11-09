import { Button } from 'antd';
import { ApiOutlined, SafetyCertificateOutlined, UserOutlined } from '@ant-design/icons';
import { Status } from './ui';

export type AccountProvider = 'codex' | 'claude';

type AccessProps = {
  selected: 'api' | AccountProvider;
  onAPI?: () => void;
  onAccount?: (provider: AccountProvider) => void;
  disabled?: boolean;
};

export const accessOptions = {
  api: { title: 'API / Coding Plan', detail: '公司中转、自定义服务或套餐专用 API Key。' },
  codex: { title: 'GPT 官方账号', detail: '通过官方 Codex 工具登录，单独查看授权与执行条件。' },
  claude: { title: 'Claude 官方账号', detail: '先检查专用出口条件，满足隔离要求后开放登录。' },
};

/** Toolbar segmented switch between the three ways of connecting a model. */
export function ConnectionAccess({ selected, onAPI, onAccount, disabled }: AccessProps) {
  const options = [
    { id: 'api' as const, icon: <ApiOutlined aria-hidden="true" />, action: onAPI, label: '配置 API 连接' },
    { id: 'codex' as const, icon: <UserOutlined aria-hidden="true" />, action: onAccount ? () => onAccount('codex') : undefined, label: '管理 GPT 账号' },
    { id: 'claude' as const, icon: <SafetyCertificateOutlined aria-hidden="true" />, action: onAccount ? () => onAccount('claude') : undefined, label: '查看 Claude 接入条件' },
  ];
  return <nav className="access-switch" aria-label="选择接入方式">
    {options.map(option => {
      const isSelected = selected === option.id;
      const body = <>{option.icon}<span>{accessOptions[option.id].title}</span></>;
      if (!option.action) return <span key={option.id} className={`access-item${isSelected ? ' is-selected' : ' is-unavailable'}`}
        aria-current={isSelected ? 'page' : undefined} title={accessOptions[option.id].detail}>{body}</span>;
      return <button type="button" key={option.id} className={`access-item${isSelected ? ' is-selected' : ''}`} aria-label={option.label}
        aria-current={isSelected ? 'page' : undefined} title={accessOptions[option.id].detail} disabled={disabled} onClick={option.action}>{body}</button>;
    })}
  </nav>;
}

export function ConnectionProgress({ saved, dirty, checked, tested, onRoles }: {
  saved: boolean; dirty: boolean; checked: boolean; tested: boolean; onRoles?: () => void;
}) {
  const steps = [
    { title: '保存配置', done: saved && !dirty, text: saved && !dirty ? '已加密保存' : saved ? '有未保存修改' : '尚未保存', warn: saved && dirty },
    { title: '检查格式', done: checked || tested, text: checked || tested ? '本次配置格式有效' : '尚未检查当前配置' },
    { title: '验证工具回合', done: tested, text: tested ? '本次工具验证通过' : '当前配置尚未验证' },
  ];
  return <section className="progress-strip" aria-label="连接配置与验证状态">
    <ol>
      {steps.map(step => <li key={step.title} className={step.done ? 'is-done' : undefined}>
        <strong>{step.title}</strong>
        <Status tone={step.done ? 'green' : step.warn ? 'orange' : 'gray'}>{step.text}</Status>
      </li>)}
      <li>
        <strong>绑定角色</strong>
        <Button size="small" disabled={!saved || dirty || !onRoles} onClick={onRoles}>前往配置角色</Button>
      </li>
    </ol>
    <small>检查和验证结果仅对应本次表单；修改配置后需重新核对。已保存不代表模型已连通。工具验证会消耗 API 额度。</small>
  </section>;
}
