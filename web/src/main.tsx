import { StyleProvider } from '@ant-design/cssinjs';
import { ConfigProvider, Layout, Typography, theme } from 'antd';
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';

import './index.css';

function App() {
  return (
    <ConfigProvider theme={{ algorithm: theme.defaultAlgorithm }}>
      <Layout className="min-h-screen">
        <Layout.Header className="flex items-center">
          <Typography.Text className="!text-white" strong>
            Relist Panel
          </Typography.Text>
        </Layout.Header>
        <Layout.Content className="flex items-center justify-center p-8">
          <Typography.Title level={3}>Relist Panel</Typography.Title>
        </Layout.Content>
      </Layout>
    </ConfigProvider>
  );
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <StyleProvider layer>
      <App />
    </StyleProvider>
  </StrictMode>,
);
