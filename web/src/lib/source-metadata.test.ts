import test from 'node:test';
import assert from 'node:assert/strict';
import {sourceDate,safeSourceLink,sourcePercent} from './source-metadata.ts';
test('source statistics preserve zero and date without local timezone shifts',()=>{assert.equal(sourcePercent(0),'0%');assert.equal(sourcePercent(null),'—');assert.match(sourceDate('2026-10-22'),/22 октября 2026/);assert.equal(sourceDate('garbage'),'');});
test('source links reject active schemes and credentials',()=>{assert.equal(safeSourceLink('javascript:alert(1)'),false);assert.equal(safeSourceLink('https://name:secret@example.com'),false);assert.equal(safeSourceLink('https://vk.com/topic-1_2?post=3'),true);});
