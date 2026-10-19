import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import Ajv from 'ajv';
import addFormats from 'ajv-formats';
import { describe, expect, it } from 'vitest';

const dir = join(__dirname, '../../../contracts');
const read = (p: string): any => JSON.parse(readFileSync(join(dir, p), 'utf8'));

const ajv = addFormats(new Ajv({ strict: false }));
ajv.addSchema(read('events.schema.json'), 'events');
const validate = (type: string) => ajv.getSchema(`events#/$defs/${type}`)!;

const examples: { type: string }[] = readdirSync(join(dir, 'examples')).map((f) => read(`examples/${f}`));

describe('event contract', () => {
  it('has an example for every event type', () => {
    const types = Object.keys(read('events.schema.json').$defs).filter((k) => k.includes('.'));
    expect(examples.map((e) => e.type).sort()).toEqual(types.sort());
  });

  it.each(examples.map((e): [string, object] => [e.type, e]))('%s example is valid', (type, example) => {
    const check = validate(type);
    expect(check(example), JSON.stringify(check.errors)).toBe(true);
  });

  it('rejects broken events', () => {
    const ok = read('examples/print.job.status.json');
    const check = validate('print.job.status');
    expect(check({ ...ok, payload: { ...ok.payload, state: 'exploded' } })).toBe(false);
    expect(check({ ...ok, payload: { ...ok.payload, extra: 1 } })).toBe(false);
    expect(check({ ...ok, type: 'print.job.created' })).toBe(false);
    expect(check({ ...ok, occurredAt: 'yesterday' })).toBe(false);
  });
});
