import { describe, expect, it } from 'vitest';
import { BookPage } from '../components/BookPage';
import { EventPage } from '../components/EventPage';
import { OrderView } from '../components/OrderView';
import Book, { metadata as bookMeta } from './book/page';
import Order, { metadata as orderMeta } from './order/page';
import HomePage from './page';

describe('routes', () => {
  it('map each static page to its client component', () => {
    expect(HomePage().type).toBe(EventPage);
    expect(Book().type).toBe(BookPage);
    expect(Order().type).toBe(OrderView);
    expect(bookMeta.title).toMatch(/Get tickets/);
    expect(orderMeta.robots.index).toBe(false);
  });
});
