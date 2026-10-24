"use client";
import { create } from "zustand";

/**
 * The customer the chat is working for (B12). While a person is inside a customer's view of the district the chat
 * sends its messages "for" that customer: POST /messages carries customer_id and the backend scopes any request the
 * turn creates to it (its tasks only see that customer's memory, files and artifacts). The chip in the chat input
 * shows it and can clear it; leaving the customer's view clears it too.
 */
export interface ChatCustomer { id: string; name: string; color: string }

export const useChatCustomer = create<{ customer: ChatCustomer | null; set: (c: ChatCustomer | null) => void }>((set) => ({
  customer: null,
  set: (customer) => set({ customer }),
}));
