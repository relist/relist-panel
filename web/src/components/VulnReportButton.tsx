import { DownloadOutlined, DownOutlined, ExportOutlined } from '@ant-design/icons';
import { App, Button, Dropdown, Space } from 'antd';
import { useState } from 'react';

export interface VulnReport {
  markdown: string;
  filename: string;
}

function download({ markdown, filename }: VulnReport) {
  const url = URL.createObjectURL(new Blob([markdown], { type: 'text/markdown;charset=utf-8' }));
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 0);
}

/**
 * Copies the report. A ClipboardItem takes the pending text, so the write
 * starts inside the click: Safari refuses one that starts after the fetch.
 */
async function copy(report: Promise<VulnReport>) {
  if (typeof ClipboardItem !== 'undefined' && navigator.clipboard.write) {
    const text = report.then((r) => new Blob([r.markdown], { type: 'text/plain' }));
    await navigator.clipboard.write([new ClipboardItem({ 'text/plain': text })]);
    return;
  }
  await navigator.clipboard.writeText((await report).markdown);
}

/**
 * Export report: copies a Markdown vulnerability report for an AI agent, or
 * downloads it as a .md file from the menu. `load` builds the report on click,
 * so its findings are fetched only when asked for.
 */
export function VulnReportButton({ disabled, load, size }: { disabled?: boolean; load: () => Promise<VulnReport>; size?: 'small' }) {
  const { message } = App.useApp();
  const [busy, setBusy] = useState(false);

  const run = async (deliver: (report: Promise<VulnReport>) => Promise<string>) => {
    setBusy(true);
    try {
      message.success(await deliver(load()));
    } catch (reason) {
      message.error(reason instanceof Error ? `Could not export the report: ${reason.message}` : 'Could not export the report');
    } finally {
      setBusy(false);
    }
  };

  // The clipboard exists only in a secure context (HTTPS or localhost); over
  // plain HTTP the report is downloaded instead.
  const copyOrDownload = async (report: Promise<VulnReport>) => {
    if (!navigator.clipboard) {
      download(await report);
      return 'Copying needs HTTPS, so the report was downloaded instead';
    }
    await copy(report);
    return 'Copied the vulnerability report';
  };
  const downloadOnly = async (report: Promise<VulnReport>) => {
    download(await report);
    return 'Downloaded the vulnerability report';
  };

  return (
    <Space.Compact size={size}>
      <Button
        disabled={disabled}
        icon={<ExportOutlined />}
        loading={busy}
        onClick={() => void run(copyOrDownload)}
      >
        Export report
      </Button>
      <Dropdown
        disabled={disabled || busy}
        menu={{
          items: [{ icon: <DownloadOutlined />, key: 'download', label: 'Download .md' }],
          onClick: () => void run(downloadOnly),
        }}
        placement="bottomRight"
        trigger={['click']}
      >
        <Button aria-label="More export options" disabled={disabled || busy} icon={<DownOutlined />} />
      </Dropdown>
    </Space.Compact>
  );
}
