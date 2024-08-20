from sqlalchemy import Column, String, Float, Date, ForeignKey, DateTime
from sqlalchemy.dialects.postgresql import UUID
from sqlalchemy.orm import relationship
from ..database.connection import Base
import uuid
from datetime import datetime, UTC

class CreditCard(Base):
    __tablename__ = "credit_cards"

    id = Column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    user_id = Column(UUID(as_uuid=True), ForeignKey("users.id"), nullable=False)
    plaid_account_id = Column(String, unique=True)
    plaid_item_id = Column(String)
    last_four_digits = Column(String(4))
    card_type = Column(String)
    issuer = Column(String)
    balance = Column(Float)
    credit_limit = Column(Float)
    available_credit = Column(Float)
    last_payment_amount = Column(Float)
    last_payment_date = Column(Date)
    next_payment_due_date = Column(Date)
    next_payment_amount = Column(Float)
    created_at = Column(DateTime, default=lambda: datetime.now(UTC))
    updated_at = Column(DateTime, default=lambda: datetime.now(UTC), onupdate=lambda: datetime.now(UTC))

    user = relationship("User", back_populates="credit_cards")
    