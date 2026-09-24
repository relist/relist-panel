import { Button, Flex, Result } from 'antd';
import { useNavigate } from 'react-router';

import { paths } from '../app/paths';

/** The design's not-found: it names the likely cause and offers a way back. */
export function NotFoundPage() {
  const navigate = useNavigate();
  return (
    <Result
      extra={
        <Flex gap={8}>
          <Button onClick={() => navigate(paths.overview)} type="primary">
            Back to Overview
          </Button>
          <Button onClick={() => navigate(paths.projects)}>All Projects</Button>
        </Flex>
      }
      status="404"
      style={{ paddingTop: 64 }}
      subTitle="That path does not exist in the panel. It may be a stale bookmark, or a Project that has since been deleted."
      title="Page not found"
    />
  );
}
