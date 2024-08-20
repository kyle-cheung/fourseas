from pydantic import BaseModel, ConfigDict
from datetime import date, datetime
from uuid import UUID
from typing import Optional

class CreditCardBase(BaseModel):
    plaid_account_id: str
    plaid_item_id: str
    last_four_digits: str
    card_type: str
    issuer: str
    balance: float
    credit_limit: float
    available_credit: float
    last_payment_amount: Optional[float] = None
    last_payment_date: Optional[date] = None
    next_payment_due_date: Optional[date] = None
    next_payment_amount: Optional[float] = None

class CreditCardCreate(CreditCardBase):
    user_id: UUID

class CreditCardUpdate(BaseModel):
    balance: Optional[float] = None
    available_credit: Optional[float] = None
    last_payment_amount: Optional[float] = None
    last_payment_date: Optional[date] = None
    next_payment_due_date: Optional[date] = None
    next_payment_amount: Optional[float] = None

class CreditCardInDB(CreditCardBase):
    id: UUID
    user_id: UUID
    created_at: datetime
    updated_at: datetime

    model_config = ConfigDict(from_attributes=True)

class CreditCardResponse(CreditCardInDB):
    pass