import { Checkbox, Form, Radio, TimePicker } from 'antd';
import dayjs, { type Dayjs } from 'dayjs';

const WEEKDAYS = [
  { label: 'Mon', value: 'mon' },
  { label: 'Tue', value: 'tue' },
  { label: 'Wed', value: 'wed' },
  { label: 'Thu', value: 'thu' },
  { label: 'Fri', value: 'fri' },
  { label: 'Sat', value: 'sat' },
  { label: 'Sun', value: 'sun' },
];

/** The form fields ScheduleFields owns. */
export interface ScheduleFormValues {
  frequency: 'daily' | 'weekly';
  weekdays: string[];
  time: Dayjs;
}

function toTime(minute: number): Dayjs {
  return dayjs().hour(Math.floor(minute / 60)).minute(minute % 60).second(0);
}

/** The form values for a schedule's days and minute of the day. */
export function scheduleFormValues(days: string[], minute: number): ScheduleFormValues {
  const daily = days.includes('daily');
  return { frequency: daily ? 'daily' : 'weekly', time: toTime(minute), weekdays: daily ? [] : days };
}

/** The days and minute a form's values stand for. */
export function scheduleFromValues(values: ScheduleFormValues): { days: string[]; minute: number } {
  return {
    days: values.frequency === 'daily' ? ['daily'] : values.weekdays,
    minute: values.time.hour() * 60 + values.time.minute(),
  };
}

/**
 * When a schedule runs: Daily or chosen weekdays, at a time in the panel's
 * time zone. Render it inside a Form; it owns the frequency, weekdays and
 * time fields (see scheduleFormValues and scheduleFromValues).
 */
export function ScheduleFields({ timeZone }: { timeZone: string }) {
  const form = Form.useFormInstance();
  const frequency = Form.useWatch('frequency', form);
  return (
    <>
      <Form.Item label="Runs" name="frequency">
        <Radio.Group
          options={[
            { label: 'Daily', value: 'daily' },
            { label: 'Weekly', value: 'weekly' },
          ]}
        />
      </Form.Item>
      {frequency === 'weekly' && (
        <Form.Item name="weekdays" rules={[{ message: 'Choose at least one day', min: 1, required: true, type: 'array' }]}>
          <Checkbox.Group options={WEEKDAYS} />
        </Form.Item>
      )}
      <Form.Item extra={`Panel time zone: ${timeZone}`} label="At" name="time">
        <TimePicker allowClear={false} format="HH:mm" minuteStep={5} needConfirm={false} />
      </Form.Item>
    </>
  );
}
